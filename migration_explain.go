package bubblelint

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Explanations describe why analysis withheld a structural candidate. They are
// informational diagnostics, never migration recommendations or fixes.
func (c *checker) explainMigrationCandidate(body *ast.BlockStmt) {
	if c.effects == nil || !c.supportsSynctest() {
		return
	}
	reasons := c.effects.reasons[body].ordered()
	if scope := c.reviewScopes[body]; scope != nil {
		reasons = scope.result.reasons.ordered()
	}
	if len(reasons) == 0 {
		return
	}
	reachable := c.reviewReachable(body)
	worker := false
	var candidate ast.Node
	for _, stmt := range body.List {
		if !reachable[stmt] {
			continue
		}
		switch stmt := stmt.(type) {
		case *ast.GoStmt:
			worker = true
		case *ast.SelectStmt:
			if worker {
				if _, timeout := c.reviewTimeout(stmt); timeout != nil && candidate == nil {
					candidate = timeout
				}
			}
		case *ast.ExprStmt:
			if call, ok := ast.Unparen(stmt.X).(*ast.CallExpr); worker && ok && c.named(call.Fun, "time", "Sleep") && candidate == nil {
				candidate = call
			}
		}
	}
	if candidate == nil {
		return
	}
	for code := range c.reported[candidate.Pos()] {
		if code == "ST100" || code == "ST101" || code == "ST102" {
			return
		}
	}
	const category = "migration"
	if c.reported[candidate.Pos()] == nil {
		c.reported[candidate.Pos()] = make(map[string]bool)
	}
	if c.reported[candidate.Pos()][category] {
		return
	}
	c.reported[candidate.Pos()][category] = true
	status := "unproven"
	primary := reasons[0]
	for _, reason := range reasons {
		if reason.Kind.blocked() && !reason.Conditional {
			status, primary = "blocked", reason
			break
		}
	}
	message := "migration " + status + ": " + primary.Message
	if primary.Conditional {
		message = "migration " + status + ": conditional path: " + primary.Message
	}
	if len(primary.Trace) != 0 {
		var symbols []string
		for _, frame := range primary.Trace {
			symbols = append(symbols, frame.Symbol)
		}
		message += " via " + strings.Join(symbols, " -> ")
	}
	if len(reasons) > 1 {
		message += fmt.Sprintf(" (%d additional reasons; see related information)", len(reasons)-1)
	}
	diagnostic := analysis.Diagnostic{
		Pos: candidate.Pos(), End: candidate.End(), Category: category, Message: message,
		Related: c.migrationEntryInformation(candidate),
	}
	diagnostic.Related = append(diagnostic.Related, c.migrationContextInformation(candidate)...)
	if scope := c.reviewScope(candidate); scope != nil {
		diagnostic.Related = append(diagnostic.Related, scope.information()...)
	}
	for _, reason := range reasons {
		message := reason.Message
		if reason.Conditional {
			message = "conditional path: " + message
		}
		for _, frame := range reason.Trace {
			if pos := c.migrationPosition(frame.Position); pos.IsValid() {
				diagnostic.Related = append(diagnostic.Related, analysis.RelatedInformation{
					Pos: pos, Message: message + ": " + frame.Symbol,
				})
			}
		}
	}
	c.pass.Report(diagnostic)
}

func (c *checker) migrationPosition(position token.Position) token.Pos {
	var result token.Pos
	c.pass.Fset.Iterate(func(file *token.File) bool {
		if file.Name() != position.Filename || position.Line < 1 || position.Line > file.LineCount() {
			return true
		}
		pos := file.LineStart(position.Line) + token.Pos(position.Column-1)
		if pos <= token.Pos(file.Base()+file.Size()) {
			result = pos
		}
		return false
	})
	return result
}
