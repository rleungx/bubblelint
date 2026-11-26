package bubblelint

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func ssaCallObject(call *ssa.CallCommon) *types.Func {
	if call.IsInvoke() {
		return call.Method
	}
	if callee := call.StaticCallee(); callee != nil {
		fn, _ := callee.Object().(*types.Func)
		return fn
	}
	return nil
}

func atomicOperation(call *ssa.CallCommon) string {
	return migrationAtomicOperation(ssaCallObject(call))
}

func (c *migrationSSAContext) resolve(call ssa.CallInstruction, seen map[migrationSSAQuery]bool) (*ssa.Function, []migrationSSABinding, []migrationSSABinding) {
	common := call.Common()
	fn := c.value(common.Value, call, seen)
	var args, free []migrationSSABinding
	if common.IsInvoke() {
		receiver := fn
		if receiver.actual != nil {
			receiver = *receiver.actual
		}
		if receiver.typ == nil {
			return nil, nil, nil
		}
		selection := c.machine.program.MethodSets.MethodSet(receiver.typ).Lookup(common.Method.Pkg(), common.Method.Name())
		if selection == nil {
			return nil, nil, nil
		}
		// SSA supplies receiver adjustments for promoted and value methods.
		fn.function = c.machine.program.MethodValue(selection)
		args = append(args, migrationSSABinding{context: c, expression: common.Value, at: call, unwrap: true})
	}
	for _, arg := range common.Args {
		args = append(args, migrationSSABinding{context: c, expression: arg, at: call})
	}
	for _, capture := range fn.bindings {
		free = append(free, migrationSSABinding{context: fn.closure, expression: capture, at: fn.closureSite})
	}
	return fn.function, args, free
}

// These contracts describe public Go APIs. They do not depend on a library's
// private fields or treat arbitrary imported code as pure.
func (c *migrationSSAContext) modeled(call ssa.CallInstruction) bool {
	if _, ok := call.Common().Value.(*ssa.Builtin); ok {
		return true
	}
	fn := ssaCallObject(call.Common())
	if fn == nil || fn.Pkg() == nil {
		return false
	}
	if fn.Pkg().Path() == "sync/atomic" && atomicOperation(call.Common()) != "" {
		return true
	}
	_, ok := c.standardValue(call, make(map[migrationSSAQuery]bool))
	if ok {
		return true
	}
	return knownMigrationCall(fn) && fn.Pkg().Path() != "testing" && fn.Name() != "AfterFunc" && fn.Name() != "Go"
}

func (c *migrationSSAContext) standardValue(call ssa.CallInstruction, seen map[migrationSSAQuery]bool) (migrationSSAValue, bool) {
	common := call.Common()
	fn := ssaCallObject(common)
	if fn == nil || fn.Pkg() == nil {
		return migrationSSAValue{}, false
	}
	path, name := fn.Pkg().Path(), fn.Name()
	if atomicOperation(common) == "Load" && len(common.Args) > 0 {
		receiver := c.value(common.Args[0], call, seen)
		container := fn.Type().(*types.Signature).Recv() != nil
		return c.machine.read(receiver.location, c, call, fn.Type().(*types.Signature).Results().At(0).Type(), container, seen), true
	}
	if path == "testing" && name == "Context" && len(common.Args) == 1 && c.value(common.Args[0], call, seen).bubbleTestingT {
		return migrationSSAValue{contextKnown: true, contextHasDone: true}, true
	}
	if path == "context" {
		switch name {
		case "Background", "TODO":
			return migrationSSAValue{contextKnown: true}, true
		case "WithCancel":
			if len(common.Args) == 1 && c.value(common.Args[0], call, seen).contextKnown {
				return migrationSSAValue{tuple: []migrationSSAValue{{contextKnown: true, contextHasDone: true}, {cancel: true}}}, true
			}
		case "Done":
			receiver := c.value(common.Value, call, seen)
			if receiver.contextKnown {
				if receiver.contextHasDone {
					return migrationSSAValue{ownedChannel: true, nilKnown: true}, true
				}
				return migrationSSAValue{nilKnown: true, isNil: true}, true
			}
		}
	}
	if path == "time" && (name == "After" || name == "Tick") {
		return migrationSSAValue{ownedChannel: true, nilKnown: true}, true
	}
	return migrationSSAValue{}, false
}

func (c *migrationSSAContext) callValue(call *ssa.Call, seen map[migrationSSAQuery]bool) migrationSSAValue {
	if value, modeled := c.standardValue(call, seen); modeled {
		return value
	}
	if child := c.children[call]; child != nil {
		var returns [][]migrationSSAValue
		for _, block := range child.function.Blocks {
			if len(block.Instrs) == 0 {
				continue
			}
			ret, ok := block.Instrs[len(block.Instrs)-1].(*ssa.Return)
			if !ok {
				continue
			}
			var values []migrationSSAValue
			for _, v := range ret.Results {
				values = append(values, child.value(v, ret, seen))
			}
			returns = append(returns, values)
		}
		if len(returns) == 0 {
			return migrationSSAValue{}
		}
		result := make([]migrationSSAValue, len(returns[0]))
		for i := range result {
			var values []migrationSSAValue
			for _, returned := range returns {
				if i < len(returned) {
					values = append(values, returned[i])
				}
			}
			result[i] = migrationSSAJoin(values)
		}
		if len(result) == 1 {
			return result[0]
		}
		return migrationSSAValue{tuple: result}
	}
	return migrationSSAValue{}
}

func (c *migrationSSAContext) reason(result *migrationSSAResult, kind migrationEffect, pos token.Pos, symbol, message string) {
	reason := migrationReason{Kind: kind, Message: message, Trace: []migrationFrame{{Symbol: symbol, Position: c.machine.c.pass.Fset.PositionFor(pos, false)}}}
	result.reasons.add(reason)
	if result.scoped != nil {
		result.scoped.reasons.add(reason)
	}
}

func (c *migrationSSAContext) summary() migrationSSAResult {
	if body := migrationSSABody(c.function); body != nil {
		if reasons, known := c.machine.base.reasons[body]; known {
			return migrationSSAResult{reasons: reasons, timer: c.machine.base.timers[body]}
		}
	}
	result := migrationSSAResult{reasons: make(migrationReasonSet)}
	c.reason(&result, effectOpaqueCall, c.function.Pos(), c.function.String(), "the function has no usable effect summary")
	return result
}

func (c *migrationSSAContext) fallback(call ssa.CallInstruction) migrationSSAResult {
	result := migrationSSAResult{reasons: make(migrationReasonSet)}
	fn := ssaCallObject(call.Common())
	dispatched := false
	if call.Common().IsInvoke() {
		if target, _, _ := c.resolve(call, make(map[migrationSSAQuery]bool)); target != nil {
			if concrete, ok := target.Object().(*types.Func); ok {
				fn, dispatched = concrete, true
			}
		}
	}
	if fn == nil {
		if c.value(call.Common().Value, call, make(map[migrationSSAQuery]bool)).cancel {
			return result
		}
		c.reason(&result, effectOpaqueCall, call.Pos(), "function value", "the function-value call target is unknown")
		return result
	}
	if body, local := c.machine.c.functions[fn.Origin()]; local {
		for _, reason := range c.machine.base.reasons[body.body].ordered() {
			result.reasons.add(reason)
		}
		result.timer = c.machine.base.timers[body.body]
		return result
	}
	temporary := *c.machine.c
	temporary.effects = &migrationEffectsResult{reasons: make(map[*ast.BlockStmt]migrationReasonSet), timers: make(map[*ast.BlockStmt]bool), imported: c.machine.base.imported}
	temporary.effectConditional = false
	body := &ast.BlockStmt{}
	callAST := c.machine.calls[call.Pos()]
	if callAST == nil {
		callAST = &ast.CallExpr{Fun: &ast.Ident{Name: fn.Name(), NamePos: call.Pos()}, Lparen: call.Pos()}
	}
	specialized := false
	if !dispatched {
		specialized, _ = temporary.specializeMigrationCall(body, callAST, true)
	}
	if specialized {
		result.timer = temporary.effects.timers[body]
	} else if fact := temporary.migrationInvocationEffects(body, callAST, fn); fact != nil {
		result.timer = fact.Timer
	}
	for _, reason := range temporary.effects.reasons[body].ordered() {
		result.reasons.add(reason)
	}
	result.timer = result.timer || migrationTimerOperation(fn)
	return result
}

func (c *migrationSSAContext) callEffects(call ssa.CallInstruction) migrationSSAResult {
	result := migrationSSAResult{reasons: make(migrationReasonSet)}
	common := call.Common()
	fn := ssaCallObject(common)
	if fn != nil && knownMigrationCall(fn) {
		for _, arg := range common.Args {
			if _, callback := arg.Type().Underlying().(*types.Signature); callback {
				// Modeled external operations still need a callback lifetime
				// contract before their object state can be refined.
				c.retainSummary = true
				c.machine.notes[call.Pos()] = "callback lifetime is not modeled; its caller retains the conservative function summary"
				return c.fallback(call)
			}
		}
	}
	if atomicOperation(common) != "" {
		return result
	}
	if _, ok := c.standardValue(call, make(map[migrationSSAQuery]bool)); ok {
		if fn != nil {
			result.timer = migrationTimerOperation(fn)
		}
		return result
	}
	if builtin, ok := common.Value.(*ssa.Builtin); ok {
		if builtin.Name() == "close" && len(common.Args) > 0 && !c.value(common.Args[0], call, make(map[migrationSSAQuery]bool)).ownedChannel {
			c.reason(&result, effectLifecycle, call.Pos(), "close", "the channel's bubble ownership has not been established")
		}
		return result
	}
	if child := c.children[call]; child != nil {
		actual, _, _ := c.resolve(call, make(map[migrationSSAQuery]bool))
		if actual != child.function {
			c.machine.failed = true
			c.machine.failure = "call resolution changed after mutation discovery"
			return result
		}
		callee := child.analyze()
		frame := migrationFrame{Symbol: child.function.String(), Position: c.machine.c.pass.Fset.PositionFor(call.Pos(), false)}
		for _, reason := range callee.reasons.ordered() {
			reason.Trace = append([]migrationFrame{frame}, reason.Trace...)
			result.reasons.add(reason)
		}
		result.timer = callee.timer
		return result
	}
	if message := c.machine.cuts[call]; message != "" {
		c.machine.notes[call.Pos()] = message
	}
	return c.fallback(call)
}

func (c *migrationSSAContext) analyze() migrationSSAResult {
	if c.result != nil {
		return *c.result
	}
	result := migrationSSAResult{reasons: make(migrationReasonSet)}
	if c.parent == nil && c.machine.scope != nil {
		result.scoped = &migrationSSAResult{reasons: make(migrationReasonSet)}
	}
	if c.analyzing {
		c.reason(&result, effectRecursion, c.function.Pos(), c.function.String(), "recursive termination has not been established")
		return result
	}
	c.analyzing = true
	defer func() { c.analyzing = false }()
	reachable := make(map[*ssa.BasicBlock]bool)
	successors := make(map[*ssa.BasicBlock][]*ssa.BasicBlock)
	effectsAt := make(map[ssa.CallInstruction]migrationSSAResult)
	queue := []*ssa.BasicBlock{c.function.Blocks[0]}
	for len(queue) > 0 && !c.machine.failed {
		block := queue[0]
		queue = queue[1:]
		if reachable[block] {
			continue
		}
		reachable[block] = true
		for _, instruction := range block.Instrs {
			if !c.machine.spend() {
				break
			}
			operands := instruction.Operands(nil)
			for _, operand := range operands {
				if global, ok := (*operand).(*ssa.Global); ok {
					c.reason(&result, effectShared, instruction.Pos(), global.String(), "package-level state has not been isolated")
				}
			}
			if call, ok := instruction.(ssa.CallInstruction); ok {
				effects := c.callEffects(call)
				effectsAt[call] = effects
				for _, reason := range effects.reasons.ordered() {
					result.reasons.add(reason)
					if result.scoped != nil && call.Pos() >= c.machine.scope.start {
						result.scoped.reasons.add(reason)
					}
				}
				result.timer = result.timer || effects.timer
				if result.scoped != nil && call.Pos() >= c.machine.scope.start {
					result.scoped.timer = result.scoped.timer || effects.timer
				}
			}
			switch instruction := instruction.(type) {
			case *ssa.Panic:
				c.reason(&result, effectOpaqueCall, instruction.Pos(), "panic", "a reachable panic needs a migration review")
			case *ssa.UnOp:
				if instruction.Op == token.ARROW {
					channel := c.value(instruction.X, instruction, make(map[migrationSSAQuery]bool))
					if !channel.ownedChannel && !channel.isNil {
						c.reason(&result, effectLifecycle, instruction.Pos(), "channel receive", "the channel's bubble ownership has not been established")
					}
				}
			case *ssa.Select:
				for _, state := range instruction.States {
					channel := c.value(state.Chan, instruction, make(map[migrationSSAQuery]bool))
					if !channel.ownedChannel && !channel.isNil {
						c.reason(&result, effectLifecycle, state.Pos, "select", "the channel's bubble ownership has not been established")
					}
				}
			case *ssa.Send:
				if !c.value(instruction.Chan, instruction, make(map[migrationSSAQuery]bool)).ownedChannel {
					c.reason(&result, effectLifecycle, instruction.Pos(), "channel send", "the channel's bubble ownership has not been established")
				}
			}
		}
		next := block.Succs
		if branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If); ok {
			truth, known := migrationSSABoolean(c.value(branch.Cond, branch, make(map[migrationSSAQuery]bool)))
			pos := branch.Cond.Pos()
			if known {
				bit := uint8(4)
				index := 1
				if truth {
					bit = 2
					index = 0
				}
				if pos.IsValid() {
					c.machine.proofs[pos] |= bit
				}
				next = block.Succs[index : index+1]
			} else if pos.IsValid() {
				c.machine.proofs[pos] |= 1
			}
		}
		successors[block] = next
		queue = append(queue, next...)
	}
	// Required effects must dominate all reachable returns. In particular, an
	// effect after an unknown branch is not promoted to a proven obstacle.
	var blocks []*ssa.BasicBlock
	for _, block := range c.function.Blocks {
		if reachable[block] {
			blocks = append(blocks, block)
		}
	}
	required := requiredMigrationGraphBlocks(blocks, func(block *ssa.BasicBlock) []*ssa.BasicBlock { return successors[block] })
	classify := func(target *migrationSSAResult, start token.Pos) {
		for kind, reason := range target.reasons {
			reason.Conditional = true
			target.reasons[kind] = reason
		}
		for _, block := range c.function.Blocks {
			if !reachable[block] || !required[block] {
				continue
			}
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || start.IsValid() && call.Pos() < start {
					continue
				}
				effects := effectsAt[call]
				for kind, reason := range effects.reasons {
					if !reason.Conditional {
						target.reasons[kind] = reason
					}
				}
			}
		}
	}
	classify(&result, token.NoPos)
	if result.scoped != nil {
		classify(result.scoped, c.machine.scope.start)
	}
	if migrationSSACycle(successors) {
		// Loop progress and cooperative clock polling use the existing CFG
		// analysis until this refinement can preserve those contracts.
		c.retainSummary = true
		c.machine.notes[c.function.Pos()] = "loop progress uses the conservative function summary"
	}
	if c.retainSummary {
		summary := c.summary()
		for _, reason := range summary.reasons {
			result.reasons.add(reason)
			if result.scoped != nil {
				// A retained function summary cannot separate outer cleanup
				// from the proposed scope. Preserve it conservatively.
				result.scoped.reasons.add(reason)
			}
		}
		result.timer = result.timer || summary.timer
		if result.scoped != nil {
			result.scoped.timer = result.scoped.timer || summary.timer
		}
		for _, block := range c.function.Blocks {
			if branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If); ok && branch.Cond.Pos().IsValid() {
				c.machine.proofs[branch.Cond.Pos()] |= 1
			}
		}
	}
	c.result = &result
	if body := migrationSSABody(c.function); body != nil {
		previous := c.machine.results[body]
		if previous.reasons == nil {
			previous.reasons = make(migrationReasonSet)
		}
		for _, reason := range result.reasons {
			previous.reasons.add(reason)
		}
		previous.timer = previous.timer || result.timer
		c.machine.results[body] = previous
	}
	return result
}

func migrationSSACycle(graph map[*ssa.BasicBlock][]*ssa.BasicBlock) bool {
	colors := make(map[*ssa.BasicBlock]uint8)
	var visit func(*ssa.BasicBlock) bool
	visit = func(block *ssa.BasicBlock) bool {
		if colors[block] == 1 {
			return true
		}
		if colors[block] == 2 {
			return false
		}
		colors[block] = 1
		for _, next := range graph[block] {
			if visit(next) {
				return true
			}
		}
		colors[block] = 2
		return false
	}
	for block := range graph {
		if visit(block) {
			return true
		}
	}
	return false
}
