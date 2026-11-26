package bubblelint

import (
	"go/types"
	"strings"
)

// These public operations do not introduce external blocking effects. Unknown
// update results still invalidate value knowledge; effect safety is separate
// from knowing the value an update returns or stores.
func migrationAtomicOperation(fn *types.Func) string {
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "sync/atomic" {
		return ""
	}
	name := fn.Name()
	if fn.Type().(*types.Signature).Recv() == nil {
		matched := false
		for _, suffix := range []string{"Int32", "Int64", "Uint32", "Uint64", "Uintptr", "Pointer"} {
			if prefix, ok := strings.CutSuffix(name, suffix); ok {
				name, matched = prefix, true
				break
			}
		}
		if !matched {
			return ""
		}
	}
	switch name {
	case "Load", "Store", "Add", "Swap", "CompareAndSwap", "And", "Or":
		return name
	}
	return ""
}
