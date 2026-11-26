package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/cfg"
)

// Summaries preserve both known external effects and unresolved behavior. Clock
// and timer evidence propagates through synchronous calls; reasons also follow
// workers, defers, and callbacks.
func (c *checker) findMigrationBarriers() {
	c.migrationBarriers = make(map[*ast.BlockStmt]bool)
	c.reviewBarriers = make(map[*ast.BlockStmt]bool)
	callers := make(map[*ast.BlockStmt]map[*ast.BlockStmt]bool)
	clockCallers := make(map[*ast.BlockStmt]map[*ast.BlockStmt]bool)
	effectEdges := make(map[*ast.BlockStmt][]migrationEffectEdge)
	clockReaders := make(map[*ast.BlockStmt]bool)
	graphs := make(map[*ast.BlockStmt][]*cfg.Block)
	clocks := make(map[*cfg.Block]bool)
	sleeps := make(map[*cfg.Block]bool)
	clockCalls := make(map[*cfg.Block][]*ast.BlockStmt)
	var bodies []*ast.BlockStmt
	add := func(body *ast.BlockStmt) {
		if _, known := callers[body]; !known {
			// An empty summary is distinct from a body never inspected.
			if c.effects.reasons[body] == nil {
				c.effects.reasons[body] = make(migrationReasonSet)
			}
			callers[body] = make(map[*ast.BlockStmt]bool)
			clockCallers[body] = make(map[*ast.BlockStmt]bool)
			bodies = append(bodies, body)
		}
	}
	// Production functions need summaries even in packages without tests.
	for _, fn := range c.functions {
		add(fn.body)
	}
	sort.Slice(bodies, func(i, j int) bool { return bodies[i].Pos() < bodies[j].Pos() })
	for i := 0; i < len(bodies); i++ {
		body := bodies[i]
		edge := func(callee function, call *ast.CallExpr, symbol string, block *cfg.Block, synchronous, conditional bool) {
			add(callee.body)
			callers[callee.body][body] = true
			effectEdges[callee.body] = append(effectEdges[callee.body], migrationEffectEdge{
				caller:      body,
				frame:       migrationFrame{Symbol: symbol, Position: c.pass.Fset.PositionFor(call.Pos(), false)},
				conditional: conditional,
			})
			if synchronous {
				clockCallers[callee.body][body] = true
				clockCalls[block] = append(clockCalls[block], callee.body)
			}
		}
		graphs[body] = c.migrationEffectNodes(body, func(block *cfg.Block, node ast.Node) {
			conditional := c.effectConditional
			operands := c.migrationConditionalOperands(node)
			ast.Inspect(node, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					return false
				}
				if id, ok := node.(*ast.Ident); ok {
					if obj, ok := c.pass.TypesInfo.ObjectOf(id).(*types.Var); ok && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
						c.addMigrationReason(body, effectShared, id, obj.String(), "package-level state has not been isolated")
					}
				}
				return true
			})
			if expr, ok := node.(*ast.ExprStmt); ok {
				if call, ok := ast.Unparen(expr.X).(*ast.CallExpr); ok && c.named(call.Fun, "time", "Sleep") && len(call.Args) == 1 {
					value := c.pass.TypesInfo.Types[call.Args[0]].Value
					sleeps[block] = sleeps[block] || value != nil && constant.Sign(value) > 0
				}
			}
			c.migrationCalls(node, func(call *ast.CallExpr, synchronous bool) {
				c.effectConditional = conditional || operands[call]
				target := *call
				target.Fun, _ = c.migrationFunction(call.Fun)
				fn := c.migrationCallTarget(&target)
				if specialized, clock := c.specializeMigrationCall(body, &target, synchronous); specialized {
					if synchronous && clock {
						clocks[block], clockReaders[body] = true, true
					}
					return
				}
				if fn != nil {
					if synchronous && clockOperation(fn) {
						clocks[block], clockReaders[body] = true, true
					}
					if synchronous && migrationTimerOperation(fn) {
						c.effects.timers[body] = true
					}
					if callee, ok := c.functions[fn.Origin()]; ok {
						edge(callee, call, fn.FullName(), block, synchronous, c.effectConditional)
					} else if fact := c.migrationInvocationEffects(body, &target, fn); fact != nil {
						if synchronous {
							c.effects.timers[body] = c.effects.timers[body] || fact.Timer
							for _, reason := range fact.Reasons {
								if reason.Kind == effectClock {
									clocks[block], clockReaders[body] = true, true
								}
							}
						}
					}
				} else if callee, ok := c.callable(target.Fun); ok {
					edge(callee, call, "func literal", block, synchronous, c.effectConditional)
				} else if !c.pass.TypesInfo.Types[call.Fun].IsType() && !c.pass.TypesInfo.Types[call.Fun].IsBuiltin() {
					c.addMigrationReason(body, effectOpaqueCall, call, "function value", "the function-value call target is unknown")
				}
				for _, arg := range call.Args {
					if !c.functionValue(arg) {
						continue
					}
					c.effectConditional = true
					arg, _ := c.migrationFunction(arg)
					if callee, ok := c.callable(arg); ok {
						symbol := "callback"
						if fn := c.object(arg); fn != nil {
							symbol = fn.FullName()
						}
						edge(callee, call, symbol, block, false, true)
					} else if fn := c.object(arg); fn != nil {
						c.migrationInvocationEffects(body, &ast.CallExpr{Fun: arg}, fn)
					} else {
						c.addMigrationReason(body, effectOpaqueCall, call, "callback", "the callback target is unknown")
					}
				}
			})
		})
	}
	propagateMigrationEffects(clockReaders, clockCallers)
	propagateMigrationEffects(c.effects.timers, clockCallers)
	for block, callees := range clockCalls {
		for _, callee := range callees {
			clocks[block] = clocks[block] || clockReaders[callee]
		}
	}
	for body, blocks := range graphs {
		if c.migrationClockCycle(blocks, clocks, sleeps) {
			c.addMigrationReason(body, effectPolling, body, "clock polling", "a clock-polling cycle has no established positive sleep")
		}
		all := make(map[*cfg.Block]bool)
		for _, block := range blocks {
			all[block] = true
		}
		if c.migrationClockCycle(blocks, all, nil) {
			c.addMigrationReason(body, effectLoop, body, "loop", "loop termination has not been established")
		}
	}
	recursive := make(map[*ast.BlockStmt]bool)
	markMigrationRecursion(recursive, callers)
	for body := range recursive {
		c.addMigrationReason(body, effectRecursion, body, "recursive helper", "recursive termination has not been established")
	}
	c.propagateMigrationReasons(effectEdges)
	for body, reasons := range c.effects.reasons {
		for kind := range reasons {
			c.reviewBarriers[body] = true
			if !kind.reviewOnly() {
				c.migrationBarriers[body] = true
			}
		}
	}
	c.effects.barriers, c.effects.reviewBarriers = c.migrationBarriers, c.reviewBarriers
	if c.coarseEffects == nil {
		c.effects.entries = c.migrationEntryPoints(callers)
	} else {
		c.effects.entries = c.coarseEffects.entries
	}
}

func (c *checker) knownMigrationInvocation(fn *types.Func, call *ast.CallExpr) bool {
	if !knownMigrationCall(fn) {
		return false
	}
	if fn.Pkg().Path() != "testing" {
		return true
	}
	switch fn.Name() {
	case "Error", "Errorf", "Fatal", "Fatalf", "Log", "Logf", "Skip", "Skipf":
		args := call.Args
		if method, _, methodArgs := c.methodCall(call); method != nil {
			args = methodArgs
		}
		for _, arg := range args {
			typ := c.pass.TypesInfo.TypeOf(arg)
			if _, basic := typ.Underlying().(*types.Basic); !basic || types.NewMethodSet(typ).Len() != 0 {
				// Formatting can invoke user-defined String or Error methods.
				return false
			}
		}
	}
	return true
}

func markMigrationRecursion(barriers map[*ast.BlockStmt]bool, edges map[*ast.BlockStmt]map[*ast.BlockStmt]bool) {
	visited := make(map[*ast.BlockStmt]bool)
	active := make(map[*ast.BlockStmt]int)
	var stack []*ast.BlockStmt
	var visit func(*ast.BlockStmt)
	visit = func(body *ast.BlockStmt) {
		if index, ok := active[body]; ok {
			for _, recursive := range stack[index:] {
				barriers[recursive] = true
			}
			return
		}
		if visited[body] {
			return
		}
		visited[body] = true
		active[body] = len(stack)
		stack = append(stack, body)
		for next := range edges[body] {
			visit(next)
		}
		stack = stack[:len(stack)-1]
		delete(active, body)
	}
	for body := range edges {
		visit(body)
	}
}

func propagateMigrationEffects(effects map[*ast.BlockStmt]bool, callers map[*ast.BlockStmt]map[*ast.BlockStmt]bool) {
	var pending []*ast.BlockStmt
	for body, effect := range effects {
		if effect {
			pending = append(pending, body)
		}
	}
	for i := 0; i < len(pending); i++ {
		for caller := range callers[pending[i]] {
			if !effects[caller] {
				effects[caller] = true
				pending = append(pending, caller)
			}
		}
	}
}

func (c *checker) functionValue(expr ast.Expr) bool {
	typ := c.pass.TypesInfo.TypeOf(expr)
	if typ == nil {
		return false
	}
	_, ok := typ.Underlying().(*types.Signature)
	return ok
}

func clockOperation(fn *types.Func) bool {
	if fn.Pkg() != nil && fn.Pkg().Path() == "time" {
		switch fn.Name() {
		case "Now", "Since", "Until":
			return true
		}
	}
	return false
}

// Follow immutable function results through simple local factories. Multiple
// return sites remain unknown. External factories have the same package-boundary
// limitation as direct external calls; their returned bodies are unavailable.
func (c *checker) migrationFunction(expr ast.Expr) (ast.Expr, bool) {
	seen := make(map[*ast.BlockStmt]bool)
	for {
		expr = c.resolve(expr)
		index := 0
		if id, ok := expr.(*ast.Ident); ok {
			if result, ok := c.functionResults[c.pass.TypesInfo.ObjectOf(id)]; ok {
				expr, index = result.call, result.index
			}
		}
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return expr, false
		}
		if c.pass.TypesInfo.Types[call.Fun].IsType() && len(call.Args) == 1 {
			expr = call.Args[0]
			continue
		}
		factory, ok := c.callable(call.Fun)
		if !ok {
			return expr, c.object(call.Fun) != nil
		}
		if seen[factory.body] {
			return expr, false
		}
		seen[factory.body] = true
		var result *ast.ReturnStmt
		multiple := false
		ast.Inspect(factory.body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			if ret, ok := node.(*ast.ReturnStmt); ok {
				multiple = multiple || result != nil
				result = ret
			}
			return true
		})
		if multiple || result == nil || index >= len(result.Results) {
			return expr, false
		}
		expr = result.Results[index]
	}
}

// External bodies are opaque unless their operation is modeled here. A known
// symbol is not evidence that its implementation is free of external effects.
func knownMigrationCall(fn *types.Func) bool {
	if fn.Pkg() == nil {
		return false
	}
	receiver := ""
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		t := types.Unalias(recv.Type())
		if pointer, ok := t.(*types.Pointer); ok {
			t = types.Unalias(pointer.Elem())
		}
		if named, ok := t.(*types.Named); ok {
			receiver = named.Obj().Name()
		}
	}
	switch fn.Pkg().Path() {
	case "time":
		switch receiver {
		case "":
			switch fn.Name() {
			case "Sleep", "After", "AfterFunc", "NewTimer", "NewTicker", "Tick", "Now", "Since", "Until":
				return true
			}
		case "Timer", "Ticker":
			return fn.Name() == "Stop" || fn.Name() == "Reset"
		case "Time":
			switch fn.Name() {
			case "Add", "Sub", "Before", "After", "Equal", "IsZero":
				return true
			}
		}
	case "testing":
		switch fn.Name() {
		case "Helper", "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow", "Log", "Logf", "Run", "Cleanup", "Context", "Skip", "Skipf", "SkipNow", "Short":
			return true
		}
	case "testing/synctest":
		return fn.Name() == "Test" || fn.Name() == "Run" || fn.Name() == "Wait" || fn.Name() == "Sleep"
	case "context":
		return receiver == "" && (fn.Name() == "Background" || fn.Name() == "TODO")
	case "sync":
		switch receiver {
		case "Pool":
			return fn.Name() == "Put"
		case "WaitGroup":
			switch fn.Name() {
			case "Add", "Done", "Go", "Wait":
				return true
			}
		case "Cond":
			return fn.Name() == "Wait" || fn.Name() == "Signal" || fn.Name() == "Broadcast"
		}
	case "net":
		if receiver == "" {
			switch fn.Name() {
			case "ParseIP", "ParseCIDR", "JoinHostPort", "SplitHostPort", "Pipe":
				return true
			}
		}
		return receiver == "Buffers" && fn.Name() == "Read"
	case "net/http":
		return receiver == "Header" && (fn.Name() == "Get" || fn.Name() == "Values")
	}
	return false
}

func (c *checker) migrationCallTarget(call *ast.CallExpr) *types.Func {
	fn := c.object(call.Fun)
	if fn == nil {
		return nil
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return fn
	}
	if _, ok := recv.Type().Underlying().(*types.Interface); !ok {
		return fn
	}
	_, receiver, _ := c.methodCall(call)
	if receiver == nil {
		return fn
	}
	// An immutable interface variable may have a known concrete implementation.
	// Follow its initializer and explicit interface conversions, while leaving
	// promoted interface fields and unknown parameters as opaque dispatches.
	receiver = c.resolve(receiver)
	for {
		conversion, ok := receiver.(*ast.CallExpr)
		if !ok || len(conversion.Args) != 1 || !c.pass.TypesInfo.Types[conversion.Fun].IsType() {
			break
		}
		receiver = c.resolve(conversion.Args[0])
	}
	if actual := c.pass.TypesInfo.TypeOf(receiver); actual != nil {
		method, _, _ := types.LookupFieldOrMethod(actual, true, fn.Pkg(), fn.Name())
		if target, ok := method.(*types.Func); ok {
			return target
		}
	}
	return fn
}

func networkOperation(fn *types.Func) bool {
	if fn.Pkg() == nil {
		return false
	}
	receiver := ""
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		t := types.Unalias(recv.Type())
		if pointer, ok := t.(*types.Pointer); ok {
			t = types.Unalias(pointer.Elem())
		}
		if named, ok := t.(*types.Named); ok {
			receiver = named.Obj().Name()
		}
	}
	switch fn.Pkg().Path() {
	case "net":
		switch receiver {
		case "", "Dialer", "Resolver", "TCPConn", "UDPConn", "UnixConn", "IPConn", "TCPListener", "UnixListener":
		default:
			// For example, net.Buffers.Read only reads an in-memory buffer.
			return false
		}
		// Address parsing and in-memory net.Pipe are not network I/O.
		for _, prefix := range []string{"Dial", "Listen", "Lookup", "Resolve", "Accept", "Read", "Write"} {
			if strings.HasPrefix(fn.Name(), prefix) {
				return true
			}
		}
	case "net/http":
		switch receiver {
		case "", "Client", "Transport", "Server":
		default:
			// Header.Get and other value operations do not perform a request.
			return false
		}
		switch fn.Name() {
		case "Get", "Head", "Post", "PostForm", "Do", "RoundTrip", "Serve", "ServeTLS", "ListenAndServe", "ListenAndServeTLS":
			return true
		}
	}
	return false
}
