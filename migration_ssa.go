package bubblelint

import (
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"sort"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

type migrationSSAProof struct {
	pos   token.Pos
	value bool
}
type migrationSSAResult struct {
	reasons migrationReasonSet
	timer   bool
	scoped  *migrationSSAResult
}
type migrationSSABinding struct {
	context    *migrationSSAContext
	expression ssa.Value
	at         ssa.Instruction
	unwrap     bool
}

func (b migrationSSABinding) value(seen map[migrationSSAQuery]bool) migrationSSAValue {
	v := b.context.value(b.expression, b.at, seen)
	if b.unwrap && v.actual != nil {
		return *v.actual
	}
	return v
}

type migrationSSAContext struct {
	machine         *migrationSSAMachine
	function        *ssa.Function
	parent          *migrationSSAContext
	site            ssa.CallInstruction
	async, deferred bool
	parameters      map[ssa.Value]migrationSSABinding
	objects         map[*ssa.Alloc]*migrationSSAObject
	children        map[ssa.CallInstruction]*migrationSSAContext
	result          *migrationSSAResult
	analyzing       bool
	retainSummary   bool
}

type migrationSSAWrite struct {
	context        *migrationSSAContext
	site           ssa.Instruction
	address, value ssa.Value
	atomic         bool
}
type migrationSSAEscape struct {
	context    *migrationSSAContext
	site       ssa.Instruction
	expression ssa.Value
}
type migrationSSAMachine struct {
	c              *checker
	program        *ssa.Program
	base           *migrationEffectsResult
	root           *ast.BlockStmt
	contexts       []*migrationSSAContext
	aliases        *migrationSSAAliases
	writes         []migrationSSAWrite
	escapes        []migrationSSAEscape
	escaped        []*migrationSSALocation
	proofs         map[token.Pos]uint8
	notes          map[token.Pos]string
	cuts           map[ssa.CallInstruction]string
	failure        string
	phase          string
	results        map[*ast.BlockStmt]migrationSSAResult
	calls          map[token.Pos]*ast.CallExpr
	addresses      map[migrationSSAQuery]migrationSSAValue
	stateReads     uint64
	storeTypes     map[migrationSSAStoreTypes]bool
	referenceTypes map[types.Type]bool
	scope          *migrationReviewScope
	left           int
	sealed, failed bool
}

func (m *migrationSSAMachine) spend() bool {
	if m.failed {
		return false
	}
	m.left--
	if m.left < 0 {
		m.failed = true
		m.failure = m.phase + " work limit reached"
	}
	return !m.failed
}

func migrationSSABody(fn *ssa.Function) *ast.BlockStmt {
	switch syntax := fn.Syntax().(type) {
	case *ast.FuncDecl:
		return syntax.Body
	case *ast.FuncLit:
		return syntax.Body
	}
	return nil
}

func (c *checker) hasMigrationSSACandidate(body *ast.BlockStmt) bool {
	worker, wait := false, false
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.GoStmt:
			worker = true
		case *ast.SelectStmt:
			wait = true
		case *ast.CallExpr:
			if c.named(node.Fun, "testing/synctest", "Test") || c.named(node.Fun, "testing/synctest", "Run") {
				return false
			}
			if s, ok := node.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Sleep" {
				wait = true
			}
		}
		return true
	})
	return worker && wait
}

// Refine only test-specific results. Exported helper summaries must continue to
// describe every caller, rather than inheriting one test's initial object state.
func (c *checker) refineMigrationContexts() {
	if c.effects == nil || c.checks&(migrationChecks|checkST102) == 0 && !c.explainMigration {
		return
	}
	var roots []*types.Func
	for fn, body := range c.functions {
		for _, root := range c.roots {
			if body.body == root.body && len(c.effects.reasons[body.body]) != 0 && c.hasMigrationSSACandidate(body.body) {
				roots = append(roots, fn)
			}
		}
	}
	if len(roots) == 0 {
		return
	}
	program := ssa.NewProgram(c.pass.Fset, ssa.InstantiateGenerics)
	created := make(map[*types.Package]bool)
	var create func(*types.Package)
	create = func(pkg *types.Package) {
		if created[pkg] {
			return
		}
		created[pkg] = true
		for _, dep := range pkg.Imports() {
			create(dep)
		}
		if pkg != c.pass.Pkg {
			program.CreatePackage(pkg, nil, nil, true)
		}
	}
	create(c.pass.Pkg)
	pkg := program.CreatePackage(c.pass.Pkg, c.pass.Files, c.pass.TypesInfo, true)
	pkg.Build()
	base := *c.effects
	base.reasons = maps.Clone(c.effects.reasons)
	base.timers = maps.Clone(c.effects.timers)
	c.coarseEffects = &base
	c.prepareMigrationPrograms()
	var entries []*ssa.Function
	byBody := make(map[*ast.BlockStmt]*ssa.Function)
	var index func(*ssa.Function)
	index = func(fn *ssa.Function) {
		if fn == nil {
			return
		}
		byBody[migrationSSABody(fn)] = fn
		for _, child := range fn.AnonFuncs {
			index(child)
		}
	}
	for _, fn := range roots {
		function := program.FuncValue(fn)
		if function == nil {
			continue
		}
		entries = append(entries, function)
		index(function)
	}
	calls := make(map[token.Pos]*ast.CallExpr)
	seen := make(map[*ssa.Function]bool)
	for _, entry := range entries {
		seen[entry] = true
	}
	for _, file := range c.pass.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			calls[call.Lparen] = call
			fn, receiver, args := c.methodCall(call)
			if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "testing" || fn.Name() != "Run" || receiver == nil || !isTestingT(c.pass.TypesInfo.TypeOf(receiver)) || len(args) != 2 {
				return true
			}
			callback, ok := c.callable(args[1])
			if !ok || !c.hasMigrationSSACandidate(callback.body) {
				return true
			}
			if function := byBody[callback.body]; function != nil && !seen[function] {
				// A subtest is a separate proposed bubble. Captures from its
				// parent remain unknown, including channels created outside it.
				entries = append(entries, function)
				seen[function] = true
			}
			return true
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Pos() < entries[j].Pos() })
	for _, function := range entries {
		m := &migrationSSAMachine{
			c: c, program: program, base: &base, root: migrationSSABody(function), left: 16384,
			proofs: make(map[token.Pos]uint8), notes: make(map[token.Pos]string),
			cuts:    make(map[ssa.CallInstruction]string),
			results: make(map[*ast.BlockStmt]migrationSSAResult), calls: calls,
			addresses: make(map[migrationSSAQuery]migrationSSAValue), storeTypes: make(map[migrationSSAStoreTypes]bool),
			referenceTypes: make(map[types.Type]bool),
		}
		m.scope = c.leadingDeferScope(m.root)
		root := m.newContext(function, nil, nil, nil, nil)
		m.phase = "mutation discovery"
		m.discover(root)
		if !m.failed {
			m.sealed = true
			m.phase = "escape analysis"
			m.stabilizeEscapes()
		}
		if !m.failed {
			m.left = 16384
			m.phase = "branch refinement"
			root.analyze()
		}
		if c.contextNotes == nil {
			c.contextNotes = make(map[*ast.BlockStmt][]analysis.RelatedInformation)
		}
		if m.failed {
			m.notes[m.root.Pos()] = "test-specific analysis stopped: " + m.failure + "; conservative function summaries are retained"
		}
		for pos, message := range m.notes {
			if pos.IsValid() {
				c.contextNotes[m.root] = append(c.contextNotes[m.root], analysis.RelatedInformation{Pos: pos, Message: message})
			}
		}
		sort.Slice(c.contextNotes[m.root], func(i, j int) bool { return c.contextNotes[m.root][i].Pos < c.contextNotes[m.root][j].Pos })
		if m.failed {
			continue
		}
		if m.scope != nil && root.result.scoped != nil {
			if c.reviewScopes == nil {
				c.reviewScopes = make(map[*ast.BlockStmt]*migrationReviewScope)
			}
			m.scope.result = root.result.scoped
			c.reviewScopes[m.root] = m.scope
		}
		for body, result := range m.results {
			if body.Pos() < m.root.Pos() || body.End() > m.root.End() {
				continue
			}
			c.effects.reasons[body] = result.reasons
			c.effects.timers[body] = result.timer
			delete(c.effects.barriers, body)
			delete(c.effects.reviewBarriers, body)
			for kind := range result.reasons {
				c.effects.reviewBarriers[body] = true
				if !kind.reviewOnly() {
					c.effects.barriers[body] = true
				}
			}
		}
		if c.contextProofs == nil {
			c.contextProofs = make(map[*ast.BlockStmt][]migrationSSAProof)
		}
		for pos, decision := range m.proofs {
			if decision == 2 || decision == 4 {
				c.contextProofs[m.root] = append(c.contextProofs[m.root], migrationSSAProof{pos, decision == 2})
			}
		}
		sort.Slice(c.contextProofs[m.root], func(i, j int) bool { return c.contextProofs[m.root][i].pos < c.contextProofs[m.root][j].pos })
	}
}

func (c *checker) migrationContextInformation(node ast.Node) []analysis.RelatedInformation {
	var related []analysis.RelatedInformation
	var boundary *ast.BlockStmt
	consider := func(body *ast.BlockStmt) {
		if node.Pos() >= body.Pos() && node.End() <= body.End() && (boundary == nil || body.End()-body.Pos() < boundary.End()-boundary.Pos()) {
			boundary = body
		}
	}
	for body := range c.contextProofs {
		consider(body)
	}
	for body := range c.contextNotes {
		consider(body)
	}
	for _, proof := range c.contextProofs[boundary] {
		value := "false"
		if proof.value {
			value = "true"
		}
		related = append(related, analysis.RelatedInformation{Pos: proof.pos, Message: "test-specific branch condition is " + value + "; the other branch is excluded"})
	}
	related = append(related, c.contextNotes[boundary]...)
	return related
}

func (m *migrationSSAMachine) newContext(fn *ssa.Function, parent *migrationSSAContext, site ssa.CallInstruction, args []migrationSSABinding, free []migrationSSABinding) *migrationSSAContext {
	if len(m.contexts) >= 128 {
		return nil
	}
	c := &migrationSSAContext{machine: m, function: fn, parent: parent, site: site, parameters: make(map[ssa.Value]migrationSSABinding), objects: make(map[*ssa.Alloc]*migrationSSAObject), children: make(map[ssa.CallInstruction]*migrationSSAContext)}
	_, c.async = site.(*ssa.Go)
	_, c.deferred = site.(*ssa.Defer)
	for i, param := range fn.Params {
		if i < len(args) {
			c.parameters[param] = args[i]
		}
	}
	for i, param := range fn.FreeVars {
		if i < len(free) {
			c.parameters[param] = free[i]
		}
	}
	m.contexts = append(m.contexts, c)
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			escape := func(value ssa.Value) { m.escapes = append(m.escapes, migrationSSAEscape{c, instruction, value}) }
			switch instruction := instruction.(type) {
			case *ssa.IndexAddr:
				if _, fixed := instruction.Index.(*ssa.Const); !fixed {
					// An unknown index may alias any element of the allocation.
					escape(instruction.X)
				}
			case *ssa.Slice:
				// Slice offsets and backing-array aliases are not modeled.
				escape(instruction.X)
			case *ssa.Convert:
				escape(instruction.X)
			case *ssa.MultiConvert:
				escape(instruction.X)
			case *ssa.TypeAssert:
				escape(instruction.X)
			case *ssa.MapUpdate:
				escape(instruction.Key)
				escape(instruction.Value)
			case *ssa.Send:
				escape(instruction.X)
			case *ssa.Select:
				for _, state := range instruction.States {
					if state.Send != nil {
						escape(state.Send)
					}
				}
			case *ssa.UnOp:
				if instruction.Op == token.MUL {
					switch instruction.Type().Underlying().(type) {
					case *types.Struct, *types.Array:
						escape(instruction.X)
					}
				}
			}
			if store, ok := instruction.(*ssa.Store); ok {
				m.writes = append(m.writes, migrationSSAWrite{context: c, site: store, address: store.Addr, value: store.Val})
			}
			if call, ok := instruction.(*ssa.Call); ok && atomicOperation(call.Common()) == "Store" && len(call.Call.Args) > 1 {
				container := ssaCallObject(call.Common()).Type().(*types.Signature).Recv() != nil
				m.writes = append(m.writes, migrationSSAWrite{context: c, site: call, address: call.Call.Args[0], value: call.Call.Args[1], atomic: container})
			}
		}
	}
	return c
}

func (m *migrationSSAMachine) discover(c *migrationSSAContext) {
	if c == nil || m.failed {
		return
	}
	for _, block := range c.function.Blocks {
		for _, instruction := range block.Instrs {
			if !m.spend() {
				return
			}
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if builtin, ok := call.Common().Value.(*ssa.Builtin); ok && builtin.Name() != "len" && builtin.Name() != "cap" && builtin.Name() != "close" {
				for _, arg := range call.Common().Args {
					m.escapes = append(m.escapes, migrationSSAEscape{c, call, arg})
				}
			}
			if method := atomicOperation(call.Common()); method != "" && method != "Load" && method != "Store" && len(call.Common().Args) > 0 {
				for _, arg := range call.Common().Args {
					m.escapes = append(m.escapes, migrationSSAEscape{c, call, arg})
				}
			}
			if atomicOperation(call.Common()) != "" || c.modeled(call) {
				// A public API model does not describe callback writes or lifetime.
				for _, arg := range call.Common().Args {
					if _, callback := arg.Type().Underlying().(*types.Signature); callback {
						m.escapes = append(m.escapes, migrationSSAEscape{c, call, arg})
					}
				}
				continue
			}
			fn, args, free := c.resolve(call, make(map[migrationSSAQuery]bool))
			if fn != nil && len(fn.Blocks) > 0 {
				recursive := false
				for p := c; p != nil; p = p.parent {
					if p.function == fn {
						recursive = true
						break
					}
				}
				if recursive {
					// Keep the callee's conservative effects and invalidate its
					// inputs instead of discarding unrelated object state.
					m.escapeCall(c, call, free)
					m.cuts[call] = "recursive call retains its conservative function summary; its inputs may be modified"
					continue
				}
				child := m.newContext(fn, c, call, args, free)
				if child == nil {
					m.escapeCall(c, call, free)
					m.cuts[call] = "call-context limit reached; this call retains its conservative function summary"
					continue
				}
				c.children[call] = child
				m.discover(child)
			} else {
				m.escapeCall(c, call, free)
			}
		}
	}
}

func (m *migrationSSAMachine) escapeCall(c *migrationSSAContext, call ssa.CallInstruction, free []migrationSSABinding) {
	for _, arg := range call.Common().Args {
		m.escapes = append(m.escapes, migrationSSAEscape{c, call, arg})
	}
	// Function values include closure captures; interface values include receivers.
	m.escapes = append(m.escapes, migrationSSAEscape{c, call, call.Common().Value})
	for _, binding := range free {
		m.escapes = append(m.escapes, migrationSSAEscape{binding.context, binding.at, binding.expression})
	}
}

func (m *migrationSSAMachine) read(location *migrationSSALocation, c *migrationSSAContext, at ssa.Instruction, typ types.Type, atomic bool, seen map[migrationSSAQuery]bool) migrationSSAValue {
	m.stateReads++
	if location == nil {
		return migrationSSAValue{}
	}
	for _, escaped := range m.escaped {
		if escaped.contains(location) || location.contains(escaped) {
			return migrationSSAValue{}
		}
	}
	var matching []migrationSSAWrite
	if m.aliases != nil {
		for _, candidate := range m.aliases.writes[location.object] {
			write := m.writes[candidate.index]
			if candidate.address.path == location.path && write.atomic == atomic {
				if !candidate.precise {
					return migrationSSAValue{}
				}
				matching = append(matching, write)
			} else if candidate.address.contains(location) && !write.atomic {
				return migrationSSAValue{}
			}
		}
		return m.readWrites(matching, c, at, typ, seen)
	}
	for _, write := range m.writes {
		pointer, ok := write.address.Type().Underlying().(*types.Pointer)
		if !ok || !m.storeMayAffect(pointer.Elem(), location.typ) {
			continue
		}
		address := write.context.value(write.address, write.site, seen).location
		if address == nil {
			continue
		}
		if address.object == location.object && address.path == location.path && write.atomic == atomic {
			matching = append(matching, write)
		} else if address.contains(location) && !write.atomic {
			return migrationSSAValue{}
		}
	}
	return m.readWrites(matching, c, at, typ, seen)
}

func (m *migrationSSAMachine) readWrites(matching []migrationSSAWrite, c *migrationSSAContext, at ssa.Instruction, typ types.Type, seen map[migrationSSAQuery]bool) migrationSSAValue {
	if len(matching) == 0 {
		if m.sealed {
			return migrationSSAZero(typ)
		}
		return migrationSSAValue{}
	}
	if len(matching) != 1 || !migrationSSAWriteBefore(matching[0], c, at) {
		return migrationSSAValue{}
	}
	write := matching[0]
	return write.context.value(write.value, write.site, seen)
}

type migrationSSAStoreTypes struct{ stored, read types.Type }

// An aggregate store may overwrite a field or array element. Other stores need
// compatible element types before their addresses can refer to the queried cell.
// Pointer conversions can change a cell's named type or struct tags.
func (m *migrationSSAMachine) storeMayAffect(stored, read types.Type) bool {
	key := migrationSSAStoreTypes{stored, read}
	if value, known := m.storeTypes[key]; known {
		return value
	}
	value := types.IdenticalIgnoreTags(stored.Underlying(), read.Underlying())
	if !value {
		switch t := stored.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < t.NumFields() && !value; i++ {
				value = m.storeMayAffect(t.Field(i).Type(), read)
			}
		case *types.Array:
			value = m.storeMayAffect(t.Elem(), read)
		}
	}
	m.storeTypes[key] = value
	return value
}

// Scalar arguments and stores cannot carry aliases to mutable program objects.
// Pointer-free aggregates have the same property; closures and interfaces do not.
func (m *migrationSSAMachine) mayCarryReference(t types.Type) bool {
	if value, known := m.referenceTypes[t]; known {
		return value
	}
	value := true
	switch t := t.Underlying().(type) {
	case *types.Basic:
		value = t.Kind() == types.UnsafePointer
	case *types.Struct:
		value = false
		for i := 0; i < t.NumFields() && !value; i++ {
			value = m.mayCarryReference(t.Field(i).Type())
		}
	case *types.Array:
		value = m.mayCarryReference(t.Elem())
	}
	m.referenceTypes[t] = value
	return value
}

func instructionDominates(a, b ssa.Instruction) bool {
	if a.Block() != b.Block() {
		return a.Block().Dominates(b.Block())
	}
	for _, instruction := range a.Block().Instrs {
		if instruction == a {
			return true
		}
		if instruction == b {
			return false
		}
	}
	return false
}

// A dominating synchronous write can establish an initial value. A write in a
// sibling goroutine or a deferred call cannot establish a happens-before edge.
func migrationSSAWriteBefore(write migrationSSAWrite, read *migrationSSAContext, at ssa.Instruction) bool {
	a, b := write.context, read
	ancestors := make(map[*migrationSSAContext]bool)
	for p := a; p != nil; p = p.parent {
		ancestors[p] = true
	}
	common := b
	for common != nil && !ancestors[common] {
		common = common.parent
	}
	if common == nil {
		return false
	}
	point := write.site
	for a != common {
		if a.async || a.deferred {
			return false
		}
		for _, block := range a.function.Blocks {
			if len(block.Instrs) > 0 {
				if exit, ok := block.Instrs[len(block.Instrs)-1].(*ssa.Return); ok && !instructionDominates(point, exit) {
					return false
				}
			}
		}
		point = a.site
		a = a.parent
	}
	for b.parent != common && b != common {
		b = b.parent
	}
	if b != common {
		at = b.site
	}
	return instructionDominates(point, at)
}
