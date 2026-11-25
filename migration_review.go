package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

type reviewWorker struct {
	completion *ast.CallExpr
	body       *ast.BlockStmt
	timer      bool
	helper     bool
}

// Review candidates connect a worker's final signal to a test receive. The
// connection does not prove that the worker can run in a bubble.
func (c *checker) findReviewCandidates(body *ast.BlockStmt, s state) {
	if !c.supportsSynctest() || c.reviewBarriers[body] {
		return
	}
	hasWorker, hasSelect := false, false
	for _, stmt := range body.List {
		switch stmt.(type) {
		case *ast.GoStmt:
			hasWorker = true
		case *ast.SelectStmt:
			hasSelect = true
		}
	}
	if !hasWorker || !hasSelect {
		return
	}
	tests := make(map[types.Object]bool)
	for obj := range s.tests {
		tests[obj] = true
	}
	// The verified protocol has its own diagnostic, even when ST101 is disabled.
	verified := make(map[*ast.CallExpr]bool)
	if protocol := c.migrationProtocol(body, tests); protocol != nil {
		for _, timeout := range protocol.timeouts {
			verified[timeout] = true
		}
	}
	reachable := c.reviewReachable(body)
	var worker *reviewWorker
	started := 0
	for i, stmt := range body.List {
		if !reachable[stmt] {
			continue
		}
		switch s := stmt.(type) {
		case *ast.GoStmt:
			started++
			worker = c.reviewWorker(s.Call)
			if worker != nil && (worker.completion.Pos() < body.Pos() || worker.completion.End() > body.End()) {
				worker = nil
			}
		case *ast.SelectStmt:
			if started != 1 || worker == nil {
				continue
			}
			observed, timeout := c.reviewTimeout(s)
			if timeout == nil || verified[timeout] {
				continue
			}
			channel := c.reviewChannel(observed)
			if channel != nil && (channel.Pos() < body.Pos() || channel.End() > body.End()) {
				continue
			}
			if channel == worker.completion {
				if worker.timer && worker.helper {
					c.report(timeout, "ST102", "review this local timer-based worker for synctest; check that virtual-time ordering preserves the assertions and cleanup")
				}
			} else if channel != nil && c.reviewJoinedLater(body.List[i+1:], worker.completion, reachable) {
				dependencies := c.reviewDependencies(body, s.Pos())
				if dependencies.workerReaches(c, worker.body, observed) {
					c.report(timeout, "ST102", "review this local channel protocol for synctest: the worker can access the observed channel and its result is received later; check signal origin and cleanup")
				}
			}
		}
	}
}

func (c *checker) reviewWorker(call *ast.CallExpr) *reviewWorker {
	lit, ok := ast.Unparen(call.Fun).(*ast.FuncLit)
	if !ok || len(call.Args) != 0 || len(lit.Type.Params.List) != 0 || len(lit.Body.List) == 0 {
		return nil
	}
	statements := lit.Body.List
	var signal ast.Expr
	if deferred, ok := statements[0].(*ast.DeferStmt); ok {
		signal = c.reviewClose(deferred.Call)
	}
	if signal == nil {
		last := statements[len(statements)-1]
		if ret, ok := last.(*ast.ReturnStmt); ok && len(ret.Results) == 0 && len(statements) > 1 {
			last = statements[len(statements)-2]
		}
		if !c.reviewReachable(lit.Body)[last] {
			return nil
		}
		switch last := last.(type) {
		case *ast.SendStmt:
			signal = last.Chan
		case *ast.ExprStmt:
			if call, ok := ast.Unparen(last.X).(*ast.CallExpr); ok {
				signal = c.reviewClose(call)
			}
		}
	}
	if signal == nil {
		return nil
	}
	channel := c.reviewChannel(signal)
	if channel == nil {
		return nil
	}
	timer, network, helper := c.reviewHelperEffects(lit.Body)
	if network {
		return nil
	}
	return &reviewWorker{channel, lit.Body, timer, helper}
}

func (c *checker) reviewClose(call *ast.CallExpr) ast.Expr {
	if builtin, ok := directObject(c.pass.TypesInfo, call.Fun).(*types.Builtin); ok && builtin.Name() == "close" && len(call.Args) == 1 {
		return call.Args[0]
	}
	return nil
}

// Timer evidence is collected only after the shared effect analysis excludes
// unknown calls. A timer in a dead branch is not evidence.
func (c *checker) reviewHelperEffects(body *ast.BlockStmt) (timer, network, helper bool) {
	if c.effects == nil {
		return false, true, false
	}
	timer, network = c.effects.timers[body], c.effects.barriers[body]
	c.migrationNodes(body, func(_ *cfg.Block, node ast.Node) {
		c.migrationCalls(node, func(call *ast.CallExpr, _ bool) {
			if _, ok := c.callable(call.Fun); ok {
				helper = true
			}
			if fn := c.migrationCallTarget(call); fn != nil {
				if _, local := c.functions[fn.Origin()]; local {
					helper = true
				}
				if c.effects.imported[fn.Origin()] != nil {
					helper = true
				}
			}
		})
	})
	return timer, network, helper
}

// Channels are identified by their make expression, so immutable aliases and
// locally initialized struct fields refer to the same allocation.
func (c *checker) reviewChannel(expr ast.Expr) *ast.CallExpr {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		if obj := c.pass.TypesInfo.ObjectOf(id); obj == nil || obj.Parent() == c.pass.Pkg.Scope() {
			return nil
		}
	}
	expr = c.resolve(expr)
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		base := c.resolve(selector.X)
		if address, ok := base.(*ast.UnaryExpr); ok && address.Op == token.AND {
			base = ast.Unparen(address.X)
		}
		literal, ok := base.(*ast.CompositeLit)
		if !ok {
			return nil
		}
		found := false
		for _, elt := range literal.Elts {
			field, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				return nil
			}
			key, ok := field.Key.(*ast.Ident)
			if ok && key.Name == selector.Sel.Name {
				expr, found = c.resolve(field.Value), true
				break
			}
		}
		if !found {
			return nil
		}
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	builtin, ok := directObject(c.pass.TypesInfo, call.Fun).(*types.Builtin)
	if !ok || builtin.Name() != "make" {
		return nil
	}
	if _, ok := c.pass.TypesInfo.TypeOf(call).Underlying().(*types.Chan); ok {
		for _, fn := range c.functions {
			if fn.body.Pos() <= call.Pos() && call.End() <= fn.body.End() {
				return call
			}
		}
	}
	return nil
}

func (c *checker) reviewJoinedLater(statements []ast.Stmt, done *ast.CallExpr, reachable map[ast.Node]bool) bool {
	for _, stmt := range statements {
		if observed := receive(stmt); reachable[stmt] && observed != nil && c.reviewChannel(observed) == done {
			return true
		}
	}
	return false
}

func (c *checker) reviewTimeout(stmt *ast.SelectStmt) (ast.Expr, *ast.CallExpr) {
	if len(stmt.Body.List) != 2 {
		return nil, nil
	}
	var observed ast.Expr
	var timeout *ast.CallExpr
	var result, failure *ast.CommClause
	for _, item := range stmt.Body.List {
		clause := item.(*ast.CommClause)
		got := receive(clause.Comm)
		if got == nil {
			return nil, nil
		}
		if call, ok := got.(*ast.CallExpr); ok && c.named(call.Fun, "time", "After") && len(call.Args) == 1 {
			value := c.pass.TypesInfo.Types[call.Args[0]].Value
			if value == nil || constant.Sign(value) <= 0 {
				return nil, nil
			}
			if _, ok := clause.Comm.(*ast.ExprStmt); !ok || timeout != nil {
				return nil, nil
			}
			timeout = call
			failure = clause
		} else if observed == nil {
			if _, ok := c.pass.TypesInfo.TypeOf(got).Underlying().(*types.Chan); !ok {
				return nil, nil
			}
			observed = got
			result = clause
		} else {
			return nil, nil
		}
	}
	// A negative assertion intentionally waits for the timer. It has a different
	// migration boundary from waiting for a worker or readiness signal.
	if result == nil || failure == nil || c.reviewAlwaysReportsFailure(result.Body) || !c.reviewAlwaysReportsFailure(failure.Body) {
		return nil, nil
	}
	return observed, timeout
}

func (c *checker) reviewAlwaysReportsFailure(statements []ast.Stmt) bool {
	fails := make(map[*cfg.Block]bool)
	blocks := c.migrationNodes(&ast.BlockStmt{List: statements}, func(block *cfg.Block, node ast.Node) {
		c.migrationCalls(node, func(call *ast.CallExpr, synchronous bool) {
			if !synchronous {
				return
			}
			if fn := c.object(call.Fun); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "testing" {
				switch fn.Name() {
				case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
					fails[block] = true
				}
			}
		})
	})
	// A conditional assertion on the received value is a normal success path.
	// Only treat a branch as failing when every reachable path reports failure.
	for changed := true; changed; {
		changed = false
		for i := len(blocks) - 1; i >= 0; i-- {
			block := blocks[i]
			if fails[block] {
				continue
			}
			successors := c.flowSuccessors(block)
			all := len(successors) != 0
			for _, next := range successors {
				all = all && fails[next]
			}
			if all {
				fails[block], changed = true, true
			}
		}
	}
	return fails[blocks[0]]
}
