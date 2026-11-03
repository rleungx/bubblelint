package bubblelint

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Fixes must be safe for every use of the edited source, not just the call path
// that produces a diagnostic. Only an inline synctest.Test callback's own,
// immutable parameter is known to always refer to a synctest T. Shared helpers
// and aliased callbacks can also be called with an ordinary T.
func (c *checker) findParallelFixes() {
	c.parallelFixes = make(map[*ast.CallExpr][]analysis.SuggestedFix)
	for _, file := range c.pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		directives := fixDirectives(file.Comments)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 || !c.directSynctestTest(call.Fun) {
				return true
			}
			callback, ok := ast.Unparen(call.Args[1]).(*ast.FuncLit)
			if !ok || len(callback.Type.Params.List) != 1 || len(callback.Type.Params.List[0].Names) != 1 {
				return true
			}
			param := c.pass.TypesInfo.Defs[callback.Type.Params.List[0].Names[0]]
			if param == nil || c.assigned[param] {
				return true
			}
			ast.Inspect(callback.Body, func(node ast.Node) bool {
				var statements []ast.Stmt
				switch n := node.(type) {
				case *ast.FuncLit:
					return false
				case *ast.BlockStmt:
					statements = n.List
				case *ast.CaseClause:
					statements = n.Body
				case *ast.CommClause:
					statements = n.Body
				}
				for _, stmt := range statements {
					c.parallelFix(stmt, param, file.Comments, directives)
				}
				return true
			})
			return true
		})
	}
}

func (c *checker) directSynctestTest(expr ast.Expr) bool {
	var name *ast.Ident
	switch fn := ast.Unparen(expr).(type) {
	case *ast.Ident:
		name = fn
	case *ast.SelectorExpr:
		name = fn.Sel
	}
	if name == nil {
		return false
	}
	// A package-level function variable can be reassigned by another package.
	// Require the actual function object, rather than following aliases.
	fn, ok := c.pass.TypesInfo.ObjectOf(name).(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == "testing/synctest" && fn.Name() == "Test"
}

func (c *checker) parallelFix(stmt ast.Stmt, param types.Object, comments, directives []*ast.CommentGroup) {
	// Requiring a whole expression statement in a statement list excludes
	// go/defer, labels, and control-flow init/post clauses.
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return
	}
	// Some drivers combine the diagnostic's adjusted filename with the edit's
	// physical offsets. Do not edit source whose location is remapped by //line.
	for _, pos := range []token.Pos{stmt.Pos(), stmt.End()} {
		if c.pass.Fset.PositionFor(pos, true) != c.pass.Fset.PositionFor(pos, false) {
			return
		}
	}
	call, ok := ast.Unparen(expr.X).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return
	}
	selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !c.named(selector, "testing", "Parallel") {
		return
	}
	receiver, ok := ast.Unparen(selector.X).(*ast.Ident)
	selection := c.pass.TypesInfo.Selections[selector]
	if !ok || c.pass.TypesInfo.ObjectOf(receiver) != param || selection == nil || selection.Kind() != types.MethodVal {
		return
	}
	// Preserve comments outside the statement and leave internal comments for
	// the user to place. Comment groups are ordered by source position.
	i := sort.Search(len(comments), func(i int) bool { return comments[i].End() > stmt.Pos() })
	if i < len(comments) && comments[i].Pos() < stmt.End() {
		return
	}
	// Removing a call can attach its preceding or trailing linter directive
	// to the next statement. Preserve the directive's original scope.
	firstLine := c.pass.Fset.PositionFor(stmt.Pos(), false).Line
	lastLine := c.pass.Fset.PositionFor(stmt.End(), false).Line
	i = sort.Search(len(directives), func(i int) bool {
		return c.pass.Fset.PositionFor(directives[i].End(), false).Line >= firstLine-1
	})
	if i < len(directives) && c.pass.Fset.PositionFor(directives[i].Pos(), false).Line <= lastLine {
		return
	}
	c.parallelFixes[call] = []analysis.SuggestedFix{{
		Message: "Remove the unsupported Parallel call",
		TextEdits: []analysis.TextEdit{{
			Pos: stmt.Pos(), End: stmt.End(),
		}},
	}}
}

func fixDirectives(comments []*ast.CommentGroup) []*ast.CommentGroup {
	var directives []*ast.CommentGroup
	for _, group := range comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(comment.Text, "//"), "/*"))
			if strings.HasPrefix(text, "nolint") || strings.HasPrefix(text, "lint:") || strings.HasPrefix(text, "go:") {
				directives = append(directives, group)
				break
			}
		}
	}
	return directives
}
