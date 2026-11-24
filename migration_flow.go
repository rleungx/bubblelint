package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

// migrationCalls visits evaluated calls, not unused literals or short-circuited
// operands. A deferred or launched callee does not read the clock at this site;
// its function expression and arguments are still evaluated synchronously.
func (c *checker) migrationCalls(node ast.Node, visit func(*ast.CallExpr, bool)) {
	ast.Inspect(node, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				c.migrationCalls(n.X, visit)
				value := c.pass.TypesInfo.Types[n.X].Value
				if value == nil || (n.Op == token.LAND) == constant.BoolVal(value) {
					c.migrationCalls(n.Y, visit)
				}
				return false
			}
		case *ast.GoStmt:
			c.migrationCallInputs(n.Call, visit)
			visit(n.Call, false)
			return false
		case *ast.DeferStmt:
			c.migrationCallInputs(n.Call, visit)
			visit(n.Call, false)
			return false
		case *ast.CallExpr:
			visit(n, true)
		}
		return true
	})
}

func (c *checker) migrationCallInputs(call *ast.CallExpr, visit func(*ast.CallExpr, bool)) {
	c.migrationCalls(call.Fun, visit)
	for _, arg := range call.Args {
		c.migrationCalls(arg, visit)
	}
}

// Summaries share the main walk's reachability rules. Range backedges with a
// finite iteration count are removed only for cycle detection, after collecting
// effects. Nested unbounded loops and gotos can still form cycles.
func (c *checker) migrationNodes(body *ast.BlockStmt, visit func(*cfg.Block, ast.Node)) []*cfg.Block {
	graph := cfg.New(body, func(call *ast.CallExpr) bool {
		stops, _ := c.flowTermination(call)
		return !stops
	})
	c.flowRanges(graph)
	seen := make(map[*cfg.Block]bool)
	pending := []*cfg.Block{graph.Blocks[0]}
	var blocks []*cfg.Block
	for i := 0; i < len(pending); i++ {
		block := pending[i]
		if seen[block] {
			continue
		}
		seen[block] = true
		blocks = append(blocks, block)
		for _, node := range block.Nodes {
			visit(block, node)
		}
		pending = append(pending, c.flowSuccessors(block)...)
	}
	for _, block := range blocks {
		for i, next := range block.Succs {
			if next.Kind != cfg.KindRangeLoop || len(next.Succs) != 2 {
				continue
			}
			stmt := next.Stmt.(*ast.RangeStmt)
			inside := block == next.Succs[0] || block.Stmt != nil && block.Stmt.Pos() >= stmt.Body.Pos() && block.Stmt.End() <= stmt.Body.End()
			if inside && c.finiteRange(stmt) {
				block.Succs[i] = next.Succs[1]
			}
		}
	}
	return blocks
}

func (c *checker) finiteRange(stmt *ast.RangeStmt) bool {
	typ := c.pass.TypesInfo.TypeOf(stmt.X)
	if typ == nil {
		return false
	}
	switch t := typ.Underlying().(type) {
	case *types.Basic:
		return t.Info()&(types.IsInteger|types.IsString) != 0
	case *types.Array, *types.Slice:
		return true
	case *types.Pointer:
		_, ok := t.Elem().Underlying().(*types.Array)
		return ok
	}
	// Maps may grow during iteration; channels and iterator functions may
	// produce forever. Their backedges are not known to be finite.
	return false
}

// Find strongly connected components after removing guaranteed sleep blocks.
// This visits every block/edge once, rather than traversing once per clock read.
func (c *checker) migrationClockCycle(blocks []*cfg.Block, clocks, sleeps map[*cfg.Block]bool) bool {
	indices := make(map[*cfg.Block]int)
	low := make(map[*cfg.Block]int)
	onStack := make(map[*cfg.Block]bool)
	var stack []*cfg.Block
	index := 0
	found := false
	var visit func(*cfg.Block)
	visit = func(block *cfg.Block) {
		index++
		indices[block], low[block] = index, index
		stack = append(stack, block)
		onStack[block] = true
		self := false
		for _, next := range c.flowSuccessors(block) {
			if sleeps[next] {
				continue
			}
			self = self || next == block
			if indices[next] == 0 {
				visit(next)
				low[block] = min(low[block], low[next])
			} else if onStack[next] {
				low[block] = min(low[block], indices[next])
			}
		}
		if low[block] != indices[block] {
			return
		}
		size, clock := 0, false
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			size++
			clock = clock || clocks[last]
			if last == block {
				break
			}
		}
		found = found || clock && (size > 1 || self)
	}
	for _, block := range blocks {
		if !sleeps[block] && indices[block] == 0 {
			visit(block)
		}
	}
	return found
}
