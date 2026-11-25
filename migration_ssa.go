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
	writes         []migrationSSAWrite
	escapes        []migrationSSAEscape
	escaped        []*migrationSSALocation
	proofs         map[token.Pos]uint8
	results        map[*ast.BlockStmt]migrationSSAResult
	calls          map[token.Pos]*ast.CallExpr
	left           int
	sealed, failed bool
}

func (m *migrationSSAMachine) spend() bool {
	m.left--
	if m.left < 0 {
		m.failed = true
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

func hasMigrationSSACandidate(body *ast.BlockStmt) bool {
	worker, wait := false, false
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.GoStmt:
			worker = true
		case *ast.SelectStmt:
			wait = true
		case *ast.CallExpr:
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
			if body.body == root.body && len(c.effects.reasons[body.body]) != 0 && hasMigrationSSACandidate(body.body) {
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
	sort.Slice(roots, func(i, j int) bool { return roots[i].Pos() < roots[j].Pos() })
	for _, fn := range roots {
		function := program.FuncValue(fn)
		if function == nil {
			continue
		}
		m := &migrationSSAMachine{c: c, program: program, base: &base, root: c.functions[fn].body, left: 16384, proofs: make(map[token.Pos]uint8), results: make(map[*ast.BlockStmt]migrationSSAResult), calls: make(map[token.Pos]*ast.CallExpr)}
		for _, file := range c.pass.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					m.calls[call.Lparen] = call
				}
				return true
			})
		}
		root := m.newContext(function, nil, nil, nil, nil)
		m.discover(root)
		m.sealed = true
		m.findEscapes()
		m.left = 16384
		root.analyze()
		if m.failed {
			continue
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
	for body, proofs := range c.contextProofs {
		if node.Pos() >= body.Pos() && node.End() <= body.End() {
			for _, proof := range proofs {
				value := "false"
				if proof.value {
					value = "true"
				}
				related = append(related, analysis.RelatedInformation{Pos: proof.pos, Message: "test-specific branch condition is " + value + "; the other branch is excluded"})
			}
		}
	}
	return related
}

func (m *migrationSSAMachine) newContext(fn *ssa.Function, parent *migrationSSAContext, site ssa.CallInstruction, args []migrationSSABinding, free []migrationSSABinding) *migrationSSAContext {
	if len(m.contexts) >= 128 {
		m.failed = true
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
			if call, ok := instruction.(*ssa.Call); ok && atomicMethod(call.Common()) == "Store" && len(call.Call.Args) > 1 {
				m.writes = append(m.writes, migrationSSAWrite{context: c, site: call, address: call.Call.Args[0], value: call.Call.Args[1], atomic: true})
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
			if method := atomicMethod(call.Common()); method != "" && method != "Load" && method != "Store" && len(call.Common().Args) > 0 {
				m.escapes = append(m.escapes, migrationSSAEscape{c, call, call.Common().Args[0]})
			}
			if atomicMethod(call.Common()) != "" || c.modeled(call) {
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
					// A partial recursive call graph cannot inventory every write.
					m.failed = true
					return
				}
				child := m.newContext(fn, c, call, args, free)
				c.children[call] = child
				m.discover(child)
			} else {
				for _, arg := range call.Common().Args {
					m.escapes = append(m.escapes, migrationSSAEscape{c, call, arg})
				}
				if call.Common().IsInvoke() {
					m.escapes = append(m.escapes, migrationSSAEscape{c, call, call.Common().Value})
				}
			}
		}
	}
}

func (m *migrationSSAMachine) read(location *migrationSSALocation, c *migrationSSAContext, at ssa.Instruction, typ types.Type, atomic bool, seen map[migrationSSAQuery]bool) migrationSSAValue {
	if location == nil {
		return migrationSSAValue{}
	}
	for _, escaped := range m.escaped {
		if escaped.contains(location) || location.contains(escaped) {
			return migrationSSAValue{}
		}
	}
	var matching []migrationSSAWrite
	for _, write := range m.writes {
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

func (m *migrationSSAMachine) findEscapes() {
	add := func(location *migrationSSALocation) bool {
		if location == nil {
			return false
		}
		for _, old := range m.escaped {
			if old.contains(location) {
				return false
			}
		}
		m.escaped = append(m.escaped, location)
		return true
	}
	var locations func(migrationSSAValue) []*migrationSSALocation
	locations = func(value migrationSSAValue) []*migrationSSALocation {
		if !m.spend() {
			return nil
		}
		var result []*migrationSSALocation
		if value.location != nil {
			result = append(result, value.location)
		}
		if value.actual != nil {
			result = append(result, locations(*value.actual)...)
		}
		for _, v := range value.choices {
			result = append(result, locations(v)...)
		}
		if value.closure != nil {
			for _, capture := range value.bindings {
				result = append(result, locations(value.closure.value(capture, value.closureSite, make(map[migrationSSAQuery]bool)))...)
			}
		}
		return result
	}
	for _, escape := range m.escapes {
		for _, location := range locations(escape.context.value(escape.expression, escape.site, make(map[migrationSSAQuery]bool))) {
			add(location)
		}
	}
	for changed := true; changed && !m.failed; {
		changed = false
		for _, write := range m.writes {
			if !m.spend() {
				return
			}
			address := write.context.value(write.address, write.site, make(map[migrationSSAQuery]bool))
			location := address.location
			if location == nil {
				for _, possible := range locations(address) {
					changed = add(possible) || changed
				}
			}
			escapes := location == nil
			for _, old := range m.escaped {
				escapes = escapes || old.contains(location)
			}
			if escapes {
				for _, stored := range locations(write.context.value(write.value, write.site, make(map[migrationSSAQuery]bool))) {
					changed = add(stored) || changed
				}
			}
		}
	}
}
