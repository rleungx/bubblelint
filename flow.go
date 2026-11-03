package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

// flowStep records a reachable CFG block and its incoming async state. Keeping
// both states separate avoids carrying work from a returning branch into the
// fallthrough branch. There are at most two steps per block.
type flowStep struct {
	block        *cfg.Block
	async        bool
	out          bool
	exit         bool
	defers       []*ast.CallExpr
	predecessors []*flowStep
}

func (c *checker) flowBody(body *ast.BlockStmt, initial state) bool {
	graph := cfg.New(body, func(call *ast.CallExpr) bool {
		stops, _ := c.flowTermination(call)
		return !stops
	})
	boundedRanges := c.flowRanges(graph)
	// go/cfg puts select receive assignments in the evaluation block and
	// repeats their first LHS in the selected case. The RHS is evaluated
	// before selecting; all LHS expressions are evaluated only in that case.
	selectAssignments := make(map[*ast.AssignStmt]bool)
	var loopStack []bool
	loopDepth := 0
	cyclicDefers, hasGoto := false, false
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			if loopStack[len(loopStack)-1] {
				loopDepth--
			}
			loopStack = loopStack[:len(loopStack)-1]
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		loop := false
		switch stmt := node.(type) {
		case *ast.ForStmt:
			loop = true
		case *ast.RangeStmt:
			loop = !boundedRanges[stmt]
		case *ast.DeferStmt:
			cyclicDefers = cyclicDefers || loopDepth > 0
		case *ast.BranchStmt:
			hasGoto = hasGoto || stmt.Tok == token.GOTO
		}
		loopStack = append(loopStack, loop)
		if loop {
			loopDepth++
		}
		if clause, ok := node.(*ast.CommClause); ok {
			if assign, ok := clause.Comm.(*ast.AssignStmt); ok {
				selectAssignments[assign] = true
			}
		}
		return true
	})
	type point struct {
		block *cfg.Block
		async bool
	}
	entry := &flowStep{block: graph.Blocks[0], async: initial.async}
	steps := map[point]*flowStep{{entry.block, entry.async}: entry}
	queue := []*flowStep{entry}
	var exits []*flowStep
	hasDefers := false
	for head := 0; head < len(queue); head++ {
		step := queue[head]
		current := initial
		current.async = step.async
		for _, node := range step.block.Nodes {
			if deferred, ok := node.(*ast.DeferStmt); ok {
				// Evaluate the function and arguments at registration, but
				// execute the call only while unwinding a reachable exit.
				c.walkCallInputs(deferred.Call, &current)
				step.defers = append(step.defers, deferred.Call)
				hasDefers = true
				continue
			}
			if assign, ok := node.(*ast.AssignStmt); ok && selectAssignments[assign] {
				for _, rhs := range assign.Rhs {
					c.walk(rhs, &current)
				}
				continue
			}
			if step.block.Kind == cfg.KindSelectCaseBody {
				clause := step.block.Stmt.(*ast.CommClause)
				if assign, ok := clause.Comm.(*ast.AssignStmt); ok && node == assign.Lhs[0] {
					for _, lhs := range assign.Lhs {
						c.walk(lhs, &current)
					}
					continue
				}
			}
			c.walk(node, &current)
		}
		step.out = current.async
		step.exit = step.block.Return() != nil
		if !step.exit && len(step.block.Succs) == 0 && len(step.block.Nodes) > 0 {
			if stmt, ok := step.block.Nodes[len(step.block.Nodes)-1].(*ast.ExprStmt); ok {
				if call, ok := ast.Unparen(stmt.X).(*ast.CallExpr); ok {
					_, step.exit = c.flowTermination(call)
				}
			}
		}
		if step.exit {
			exits = append(exits, step)
		}
		for _, successor := range c.flowSuccessors(step.block) {
			key := point{successor, current.async}
			next := steps[key]
			if next == nil {
				next = &flowStep{block: successor, async: current.async}
				steps[key] = next
				queue = append(queue, next)
			}
			next.predecessors = append(next.predecessors, step)
		}
	}
	result := false
	if !hasDefers {
		for _, exit := range exits {
			result = result || exit.out
		}
		return result
	}
	// Walk reached paths backwards, executing deferred calls in LIFO order.
	// The second async bit includes effects of later defers. This needs at
	// most four states per CFG block, even with loops or conditional defers;
	// no deferred-call subsets or stacks become part of the analysis key.
	type unwind struct {
		step  *flowStep
		async bool
	}
	var pending []unwind
	for _, exit := range exits {
		pending = append(pending, unwind{exit, exit.out})
	}
	seen := make(map[unwind]bool)
	for head := 0; head < len(pending); head++ {
		item := pending[head]
		if seen[item] {
			continue
		}
		seen[item] = true
		current := initial
		current.async = item.async
		// Unknown loop bounds may invent extra registrations in the CFG.
		// Keep misuse checks and async effects, but do not base a sleep
		// suggestion on an uncertain ordering of deferred calls.
		current.suppressSleep = current.suppressSleep || cyclicDefers || hasGoto
		processExit := false
		for i := len(item.step.defers) - 1; i >= 0; i-- {
			call := item.step.defers[i]
			c.call(call, &current)
			if stops, defers := c.flowTermination(call); stops && !defers {
				processExit = true
				break
			}
		}
		if processExit {
			continue
		}
		if item.step == entry {
			result = result || current.async
		}
		for _, predecessor := range item.step.predecessors {
			pending = append(pending, unwind{predecessor, current.async})
		}
	}
	return result
}

// flowRanges preserves the zero/one-iteration bounds of constant integer
// ranges, which the general CFG represents with an unconstrained backedge.
// It also moves assignment LHS evaluation into the chosen iteration.
func (c *checker) flowRanges(graph *cfg.CFG) map[*ast.RangeStmt]bool {
	bounded := make(map[*ast.RangeStmt]bool)
	type rangeExit struct {
		stmt       *ast.RangeStmt
		body, done *cfg.Block
	}
	single := make(map[*cfg.Block]rangeExit)
	assignments := make(map[ast.Node]bool)
	iterationInputs := make(map[*cfg.Block][]ast.Node)
	for _, loop := range graph.Blocks {
		if loop.Kind != cfg.KindRangeLoop {
			continue
		}
		stmt := loop.Stmt.(*ast.RangeStmt)
		body, done := loop.Succs[0], loop.Succs[1]
		var lhs []ast.Node
		if stmt.Key != nil {
			lhs = append(lhs, stmt.Key)
			assignments[stmt.Key] = true
		}
		if stmt.Value != nil {
			lhs = append(lhs, stmt.Value)
			assignments[stmt.Value] = true
		}
		iterationInputs[body] = lhs
		value := c.pass.TypesInfo.Types[stmt.X].Value
		if value == nil || value.Kind() != constant.Int || constant.Compare(value, token.GTR, constant.MakeInt64(1)) {
			continue
		}
		bounded[stmt] = true
		if constant.Sign(value) <= 0 {
			loop.Succs = loop.Succs[1:]
			continue
		}
		single[loop] = rangeExit{stmt, body, done}
		loop.Succs = loop.Succs[:1]
	}
	for _, block := range graph.Blocks {
		filtered := block.Nodes[:0]
		for _, node := range block.Nodes {
			if !assignments[node] {
				filtered = append(filtered, node)
			}
		}
		block.Nodes = filtered
		if inputs := iterationInputs[block]; len(inputs) != 0 {
			block.Nodes = append(inputs, block.Nodes...)
		}
		for i, successor := range block.Succs {
			exit, ok := single[successor]
			if ok && (block == exit.body || block.Stmt != nil && block.Stmt.Pos() >= exit.stmt.Body.Pos() && block.Stmt.End() <= exit.stmt.Body.End()) {
				block.Succs[i] = exit.done
			}
		}
	}
	return bounded
}

func (c *checker) flowSuccessors(block *cfg.Block) []*cfg.Block {
	if len(block.Succs) != 2 || len(block.Nodes) == 0 {
		return block.Succs
	}
	var condition ast.Expr
	switch stmt := block.Succs[0].Stmt.(type) {
	case *ast.IfStmt:
		if block.Succs[0].Kind == cfg.KindIfThen {
			condition = stmt.Cond
		}
	case *ast.ForStmt:
		if block.Succs[0].Kind == cfg.KindForBody {
			condition = stmt.Cond
		}
	}
	if condition != nil && block.Nodes[len(block.Nodes)-1] == condition {
		if value := c.pass.TypesInfo.Types[condition].Value; value != nil && value.Kind() == constant.Bool {
			if constant.BoolVal(value) {
				return block.Succs[:1]
			}
			return block.Succs[1:]
		}
	}
	return block.Succs
}

// Recognize direct calls that stop the current goroutine. The second result
// distinguishes panic/Goexit (which run defers) from process exit.
func (c *checker) flowTermination(call *ast.CallExpr) (stops, defers bool) {
	if id, ok := c.resolve(call.Fun).(*ast.Ident); ok {
		if builtin, ok := c.pass.TypesInfo.ObjectOf(id).(*types.Builtin); ok && builtin.Name() == "panic" {
			return true, true
		}
	}
	fn := c.object(call.Fun)
	if fn == nil || fn.Pkg() == nil {
		return false, false
	}
	switch fn.Pkg().Path() {
	case "runtime":
		return fn.Name() == "Goexit", fn.Name() == "Goexit"
	case "os":
		return fn.Name() == "Exit", false
	case "log":
		return fn.Name() == "Fatal" || fn.Name() == "Fatalf" || fn.Name() == "Fatalln", false
	case "testing":
		switch fn.Name() {
		case "Fatal", "Fatalf", "FailNow", "Skip", "Skipf", "SkipNow":
			return true, true
		}
	}
	return false, false
}

func (c *checker) walkCallInputs(call *ast.CallExpr, s *state) {
	c.walk(call.Fun, s)
	for _, arg := range call.Args {
		c.walk(arg, s)
	}
}

// walk evaluates expressions and individual non-control statements. Function
// bodies use flowBody so branches, returns and defers retain execution order.
func (c *checker) walk(node ast.Node, s *state) {
	ast.Inspect(node, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				c.walk(n.X, s)
				value := c.pass.TypesInfo.Types[n.X].Value
				if value == nil || (n.Op == token.LAND) == constant.BoolVal(value) {
					c.walk(n.Y, s)
				}
				return false
			}
		case *ast.GoStmt:
			c.walkCallInputs(n.Call, s)
			s.async = true
			worker := *s
			worker.background = true
			c.call(n.Call, &worker)
			return false
		case *ast.CallExpr:
			c.walkCallInputs(n, s)
			c.call(n, s)
			return false
		}
		return true
	})
}
