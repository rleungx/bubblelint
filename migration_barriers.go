package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"golang.org/x/tools/go/cfg"
)

// Summarize visible effects before reporting suggestions. Clock reads propagate
// through synchronous calls; barriers also propagate through workers/callbacks.
// Both worklists handle recursive helpers without caching partial summaries.
func (c *checker) findMigrationBarriers() {
	c.migrationBarriers = make(map[*ast.BlockStmt]bool)
	callers := make(map[*ast.BlockStmt]map[*ast.BlockStmt]bool)
	clockCallers := make(map[*ast.BlockStmt]map[*ast.BlockStmt]bool)
	clockReaders := make(map[*ast.BlockStmt]bool)
	graphs := make(map[*ast.BlockStmt][]*cfg.Block)
	clocks := make(map[*cfg.Block]bool)
	sleeps := make(map[*cfg.Block]bool)
	clockCalls := make(map[*cfg.Block][]*ast.BlockStmt)
	var bodies []*ast.BlockStmt
	add := func(body *ast.BlockStmt) {
		if _, known := callers[body]; !known {
			callers[body] = make(map[*ast.BlockStmt]bool)
			clockCallers[body] = make(map[*ast.BlockStmt]bool)
			bodies = append(bodies, body)
		}
	}
	for _, root := range c.roots {
		add(root.body)
	}
	for i := 0; i < len(bodies); i++ {
		body := bodies[i]
		edge := func(callee function, block *cfg.Block, synchronous bool) {
			add(callee.body)
			callers[callee.body][body] = true
			if synchronous {
				clockCallers[callee.body][body] = true
				clockCalls[block] = append(clockCalls[block], callee.body)
			}
		}
		graphs[body] = c.migrationNodes(body, func(block *cfg.Block, node ast.Node) {
			// Only a direct, guaranteed positive sleep breaks a polling cycle.
			if expr, ok := node.(*ast.ExprStmt); ok {
				if call, ok := ast.Unparen(expr.X).(*ast.CallExpr); ok && c.named(call.Fun, "time", "Sleep") && len(call.Args) == 1 {
					value := c.pass.TypesInfo.Types[call.Args[0]].Value
					sleeps[block] = sleeps[block] || value != nil && constant.Sign(value) > 0
				}
			}
			c.migrationCalls(node, func(call *ast.CallExpr, synchronous bool) {
				target := *call
				var external bool
				target.Fun, external = c.migrationFunction(call.Fun)
				fn := c.migrationCallTarget(&target)
				if fn != nil {
					c.migrationBarriers[body] = c.migrationBarriers[body] || migrationBarrier(fn)
					if synchronous && clockOperation(fn) {
						clocks[block], clockReaders[body] = true, true
					}
					if callee, ok := c.functions[fn.Origin()]; ok {
						edge(callee, block, synchronous)
					}
				} else if callee, ok := c.callable(target.Fun); ok {
					edge(callee, block, synchronous)
				} else if !external && !c.pass.TypesInfo.Types[call.Fun].IsType() && !c.pass.TypesInfo.Types[call.Fun].IsBuiltin() {
					// A field, container element, mutable function variable or
					// parameter can hide effects just like an interface call.
					c.migrationBarriers[body] = true
				}
				for _, arg := range call.Args {
					if !c.functionValue(arg) {
						continue
					}
					arg, external := c.migrationFunction(arg)
					if callee, ok := c.callable(arg); ok {
						edge(callee, block, false)
					} else if fn := c.object(arg); fn != nil {
						c.migrationBarriers[body] = c.migrationBarriers[body] || migrationBarrier(fn)
					} else if !external {
						c.migrationBarriers[body] = true
					}
				}
			})
		})
	}
	propagateMigrationEffects(clockReaders, clockCallers)
	for block, callees := range clockCalls {
		for _, callee := range callees {
			clocks[block] = clocks[block] || clockReaders[callee]
		}
	}
	for body, blocks := range graphs {
		if c.migrationClockCycle(blocks, clocks, sleeps) {
			c.migrationBarriers[body] = true
		}
	}
	propagateMigrationEffects(c.migrationBarriers, callers)
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

func migrationBarrier(fn *types.Func) bool {
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		if _, ok := recv.Type().Underlying().(*types.Interface); ok {
			return true
		}
	}
	// These callbacks always run outside synctest bubbles, even when their
	// registration and the triggering GC happen inside the bubble.
	if fn.Pkg() != nil && fn.Pkg().Path() == "runtime" {
		switch fn.Name() {
		case "SetFinalizer", "AddCleanup":
			return true
		}
	}
	return networkOperation(fn)
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
