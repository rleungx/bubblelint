package bubblelint

import (
	"go/ast"
	"go/types"
	"strings"
)

// A migration suggestion needs a self-contained execution context. Visible
// network operations and calls dispatched through an interface are reasons to
// withhold it: the former involve real I/O, and the latter have an implementation
// and effects we cannot determine. This is a conservative filter, not a proof
// that bodies without such calls are safe to run with virtual time.
func (c *checker) hasMigrationBarrier(body *ast.BlockStmt) bool {
	if blocked, known := c.migrationBarriers[body]; known {
		return blocked
	}
	blocked := false
	ast.Inspect(body, func(node ast.Node) bool {
		if blocked {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn := c.migrationCallTarget(call)
		if fn == nil {
			return true
		}
		sig := fn.Type().(*types.Signature)
		if recv := sig.Recv(); recv != nil {
			// This also catches promoted interface methods on concrete wrapper
			// types, method expressions, and immutable method-value aliases.
			_, blocked = recv.Type().Underlying().(*types.Interface)
		}
		blocked = blocked || networkOperation(fn)
		return !blocked
	})
	if c.migrationBarriers == nil {
		c.migrationBarriers = make(map[*ast.BlockStmt]bool)
	}
	c.migrationBarriers[body] = blocked
	return blocked
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
