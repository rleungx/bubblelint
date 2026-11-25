package bubblelint

import (
	"go/ast"
	"go/token"
	"sort"

	"golang.org/x/tools/go/analysis"
)

type migrationEntry struct {
	Name string
	Pos  token.Pos
}

// A helper may be reached from multiple tests. Keep the possible entry points
// separately from diagnostic locations and runtime subtest invocations.
func (c *checker) migrationEntryPoints(callers map[*ast.BlockStmt]map[*ast.BlockStmt]bool) map[*ast.BlockStmt][]migrationEntry {
	callees := make(map[*ast.BlockStmt][]*ast.BlockStmt)
	for callee, parents := range callers {
		for caller := range parents {
			callees[caller] = append(callees[caller], callee)
		}
	}
	result := make(map[*ast.BlockStmt][]migrationEntry)
	for _, root := range c.roots {
		var entry migrationEntry
		for fn, body := range c.functions {
			if fn != nil && body.body == root.body {
				entry = migrationEntry{Name: fn.Name(), Pos: fn.Pos()}
				break
			}
		}
		if !entry.Pos.IsValid() {
			continue
		}
		seen := make(map[*ast.BlockStmt]bool)
		pending := []*ast.BlockStmt{root.body}
		for len(pending) != 0 {
			body := pending[0]
			pending = pending[1:]
			if seen[body] {
				continue
			}
			seen[body] = true
			result[body] = append(result[body], entry)
			pending = append(pending, callees[body]...)
		}
	}
	for _, entries := range result {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Pos < entries[j].Pos })
	}
	return result
}

func (c *checker) migrationEntryInformation(node ast.Node) []analysis.RelatedInformation {
	if c.effects == nil {
		return nil
	}
	var selected *ast.BlockStmt
	for body := range c.effects.entries {
		if body.Pos() <= node.Pos() && node.End() <= body.End() && (selected == nil || body.End()-body.Pos() < selected.End()-selected.Pos()) {
			selected = body
		}
	}
	var result []analysis.RelatedInformation
	for _, entry := range c.effects.entries[selected] {
		result = append(result, analysis.RelatedInformation{Pos: entry.Pos, Message: "test entry: " + entry.Name})
	}
	return result
}
