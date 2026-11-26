package bubblelint

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// A leading defer can remain in the outer test while the remaining statements
// run in a bubble. Its arguments must not execute code or create resources.
// Other cleanup registrations remain part of the proposed bubble.
type migrationReviewScope struct {
	body   *ast.BlockStmt
	defers []*ast.DeferStmt
	start  token.Pos
	result *migrationSSAResult
}

func (c *checker) leadingDeferScope(body *ast.BlockStmt) *migrationReviewScope {
	scope := &migrationReviewScope{body: body}
	for _, statement := range body.List {
		deferred, ok := statement.(*ast.DeferStmt)
		if !ok || !c.outerDeferOperand(deferred.Call.Fun) {
			break
		}
		valid := true
		for _, argument := range deferred.Call.Args {
			valid = valid && c.outerDeferOperand(argument)
		}
		if !valid {
			break
		}
		scope.defers = append(scope.defers, deferred)
	}
	if len(scope.defers) == 0 || len(scope.defers) == len(body.List) {
		return nil
	}
	scope.start = body.List[len(scope.defers)].Pos()
	return scope
}

func (c *checker) outerDeferOperand(expression ast.Expr) bool {
	expression = ast.Unparen(expression)
	if value := c.pass.TypesInfo.Types[expression]; value.Value != nil || value.IsNil() {
		return true
	}
	if function, ok := expression.(*ast.FuncLit); ok {
		// At the start of a test, a closure can capture parameters and package
		// variables, but no resources created by the remaining statements.
		return function != nil
	}
	if fn := c.object(expression); fn != nil {
		if fn.Type().(*types.Signature).Recv() != nil {
			selector, ok := expression.(*ast.SelectorExpr)
			return ok && c.outerDeferOperand(selector.X)
		}
		return true
	}
	id, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	variable, ok := c.pass.TypesInfo.ObjectOf(id).(*types.Var)
	return ok && variable.Parent() != nil && variable.Parent() != c.pass.Pkg.Scope()
}

func (c *checker) reviewScope(node ast.Node) *migrationReviewScope {
	var selected *migrationReviewScope
	for body, scope := range c.reviewScopes {
		if scope.result != nil && scope.start <= node.Pos() && node.End() <= body.End() &&
			(selected == nil || body.End()-body.Pos() < selected.body.End()-selected.body.Pos()) {
			selected = scope
		}
	}
	return selected
}

func (s *migrationReviewScope) information() []analysis.RelatedInformation {
	result := []analysis.RelatedInformation{{Pos: s.start, Message: "proposed bubble starts here"}}
	for _, deferred := range s.defers {
		result = append(result, analysis.RelatedInformation{Pos: deferred.Pos(), End: deferred.End(), Message: "keep this leading defer in the outer test"})
	}
	return append(result, analysis.RelatedInformation{Pos: s.body.Rbrace, Message: "proposed bubble ends here"})
}
