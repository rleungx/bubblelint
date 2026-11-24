package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/cfg"
)

// Summarize visible effects before reporting any suggestions. Propagating
// barriers backwards through the call graph handles shared and recursive
// helpers without caching a partially explored cycle as safe.
func (c *checker) findMigrationBarriers() {
	c.migrationBarriers = make(map[*ast.BlockStmt]bool)
	callers := make(map[*ast.BlockStmt]map[*ast.BlockStmt]bool)
	var bodies []*ast.BlockStmt
	add := func(body *ast.BlockStmt) {
		if _, known := callers[body]; !known {
			callers[body] = make(map[*ast.BlockStmt]bool)
			bodies = append(bodies, body)
		}
	}
	for _, root := range c.roots {
		add(root.body)
	}
	for i := 0; i < len(bodies); i++ {
		body := bodies[i]
		edge := func(callee function) {
			add(callee.body)
			callers[callee.body][body] = true
		}
		var inspect func(ast.Node)
		inspect = func(node ast.Node) {
			ast.Inspect(node, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					// A declared but unused closure has no effects. Invoked or
					// passed callbacks get their own graph node below.
					return false
				}
				if binary, ok := node.(*ast.BinaryExpr); ok && (binary.Op == token.LAND || binary.Op == token.LOR) {
					inspect(binary.X)
					value := c.pass.TypesInfo.Types[binary.X].Value
					if value == nil || (binary.Op == token.LAND) == constant.BoolVal(value) {
						inspect(binary.Y)
					}
					return false
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				fn := c.migrationCallTarget(call)
				if fn != nil {
					c.migrationBarriers[body] = c.migrationBarriers[body] || migrationBarrier(fn)
					if callee, ok := c.functions[fn.Origin()]; ok {
						edge(callee)
					}
				} else if callee, ok := c.callable(call.Fun); ok {
					edge(callee)
				}
				// Treat callbacks passed to other code as potentially invoked,
				// including aliases and named callbacks. Arguments are still
				// evaluated normally by the enclosing AST walk.
				for _, arg := range call.Args {
					if callee, ok := c.callable(arg); ok {
						edge(callee)
					} else if fn := c.object(arg); fn != nil && migrationBarrier(fn) {
						c.migrationBarriers[body] = true
					}
				}
				return true
			})
		}
		if c.migrationNodes(body, inspect) {
			c.migrationBarriers[body] = true
		}
	}
	var pending []*ast.BlockStmt
	for _, body := range bodies {
		if c.migrationBarriers[body] {
			pending = append(pending, body)
		}
	}
	for i := 0; i < len(pending); i++ {
		for caller := range callers[pending[i]] {
			if !c.migrationBarriers[caller] {
				c.migrationBarriers[caller] = true
				pending = append(pending, caller)
			}
		}
	}
}

// Use the existing CFG's constant-branch and zero-range handling. Summaries
// remain context independent: unknown branches are conservatively combined.
// The result marks a reachable cycle that polls the clock without an explicit
// positive sleep on every path around that cycle.
func (c *checker) migrationNodes(body *ast.BlockStmt, visit func(ast.Node)) bool {
	graph := cfg.New(body, func(call *ast.CallExpr) bool {
		stops, _ := c.flowTermination(call)
		return !stops
	})
	c.flowRanges(graph)
	seen := make(map[*cfg.Block]bool)
	sleeps := make(map[*cfg.Block]bool)
	clocks := make(map[*cfg.Block]bool)
	pending := []*cfg.Block{graph.Blocks[0]}
	for i := 0; i < len(pending); i++ {
		block := pending[i]
		if seen[block] {
			continue
		}
		seen[block] = true
		for _, node := range block.Nodes {
			visit(node)
			// Only an unconditional expression statement in this CFG block
			// breaks the cycle. Sleep(0), Gosched, mutex locking, and a sleep
			// on just one branch cannot make virtual time advance reliably.
			if expr, ok := node.(*ast.ExprStmt); ok {
				if call, ok := ast.Unparen(expr.X).(*ast.CallExpr); ok && c.named(call.Fun, "time", "Sleep") && len(call.Args) == 1 {
					value := c.pass.TypesInfo.Types[call.Args[0]].Value
					sleeps[block] = sleeps[block] || value != nil && constant.Sign(value) > 0
				}
			}
			ast.Inspect(node, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					return false
				}
				if call, ok := node.(*ast.CallExpr); ok {
					fn := c.object(call.Fun)
					if fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "time" {
						switch fn.Name() {
						case "Now", "Since", "Until":
							clocks[block] = true
						}
					}
				}
				return true
			})
		}
		pending = append(pending, c.flowSuccessors(block)...)
	}
	for clock := range clocks {
		if sleeps[clock] {
			continue
		}
		// If a clock-reading block can reach itself after removing sleep
		// blocks, some execution can keep polling without advancing time.
		visited := make(map[*cfg.Block]bool)
		queue := append([]*cfg.Block(nil), c.flowSuccessors(clock)...)
		for i := 0; i < len(queue); i++ {
			block := queue[i]
			if sleeps[block] || visited[block] {
				continue
			}
			if block == clock {
				return true
			}
			visited[block] = true
			queue = append(queue, c.flowSuccessors(block)...)
		}
	}
	return false
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
