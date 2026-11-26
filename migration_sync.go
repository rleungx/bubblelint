package bubblelint

import "go/types"

// Pool.Put stores a value without invoking user code. Pool.Get may call New,
// and does not guarantee which value it returns. Neither operation associates
// the pool with a synctest bubble.
func migrationPoolOperation(fn *types.Func) string {
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "sync" {
		return ""
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return ""
	}
	typ := types.Unalias(recv.Type())
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, ok := typ.(*types.Named)
	if !ok || named.Obj().Name() != "Pool" {
		return ""
	}
	switch fn.Name() {
	case "Put", "Get":
		return fn.Name()
	}
	return ""
}
