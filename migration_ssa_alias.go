package bubblelint

import (
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// Alias constraints describe possible references, independently of scalar
// values, write ordering, and escape status. Inclusion only adds references;
// invalidating a value must never erase an alias needed for escape propagation.
type migrationSSAAliasKey struct {
	object *migrationSSAObject
	path   string
}

type migrationSSAAliasNode struct {
	points  map[migrationSSAAliasKey]*migrationSSALocation
	pending []*migrationSSALocation
	copies  map[*migrationSSAAliasNode]bool
	fields  []migrationSSAAliasField
	loads   []*migrationSSAAliasNode
	stores  []*migrationSSAAliasNode
	unknown bool
	queued  bool
}

type migrationSSAAliasField struct {
	index int
	typ   types.Type
	to    *migrationSSAAliasNode
}

type migrationSSAAliases struct {
	machine   *migrationSSAMachine
	values    map[migrationSSAQuery]*migrationSSAAliasNode
	cells     map[migrationSSAAliasKey]*migrationSSAAliasNode
	contained map[migrationSSAAliasKey][]*migrationSSAAliasNode
	queue     []*migrationSSAAliasNode
	writes    map[*migrationSSAObject][]migrationSSAAliasWrite
	calls     map[migrationSSAQuery]*migrationSSAContext
	stores    []migrationSSAAliasStore
}

type migrationSSAAliasStore struct{ address, value *migrationSSAAliasNode }

type migrationSSAAliasWrite struct {
	index   int
	address *migrationSSALocation
	precise bool
}

func newMigrationSSAAliasNode() *migrationSSAAliasNode {
	return &migrationSSAAliasNode{points: make(map[migrationSSAAliasKey]*migrationSSALocation), copies: make(map[*migrationSSAAliasNode]bool)}
}

func (a *migrationSSAAliases) unknown(node *migrationSSAAliasNode) {
	if node == nil || node.unknown || !a.machine.spend() {
		return
	}
	node.unknown = true
	if !node.queued {
		node.queued = true
		a.queue = append(a.queue, node)
	}
}

func (a *migrationSSAAliases) add(node *migrationSSAAliasNode, location *migrationSSALocation) {
	key := migrationSSAAliasKey{location.object, location.path}
	if _, known := node.points[key]; known || !a.machine.spend() {
		return
	}
	node.points[key] = location
	node.pending = append(node.pending, location)
	if !node.queued {
		node.queued = true
		a.queue = append(a.queue, node)
	}
}

func (a *migrationSSAAliases) copy(from, to *migrationSSAAliasNode) {
	if from == nil || to == nil || from == to || from.copies[to] {
		return
	}
	from.copies[to] = true
	if from.unknown {
		a.unknown(to)
	}
	for _, location := range from.points {
		a.add(to, location)
	}
}

func (a *migrationSSAAliases) cell(location *migrationSSALocation) *migrationSSAAliasNode {
	key := migrationSSAAliasKey{location.object, location.path}
	if a.cells[key] == nil {
		a.cells[key] = newMigrationSSAAliasNode()
		root := migrationSSAAliasKey{location.object, ""}
		a.contained[root] = append(a.contained[root], a.cells[key])
		for i := range location.path {
			if location.path[i] == '/' {
				prefix := migrationSSAAliasKey{location.object, location.path[:i+1]}
				a.contained[prefix] = append(a.contained[prefix], a.cells[key])
			}
		}
	}
	return a.cells[key]
}

func (a *migrationSSAAliases) returns(c *migrationSSAContext, call *ssa.Call, index int, to *migrationSSAAliasNode) {
	child := a.calls[migrationSSAQuery{c, call}]
	if child == nil {
		a.unknown(to)
		return
	}
	for _, block := range child.function.Blocks {
		if len(block.Instrs) == 0 {
			continue
		}
		if ret, ok := block.Instrs[len(block.Instrs)-1].(*ssa.Return); ok && index < len(ret.Results) {
			a.copy(a.value(child, ret.Results[index]), to)
		}
	}
}

func (a *migrationSSAAliases) value(c *migrationSSAContext, value ssa.Value) *migrationSSAAliasNode {
	if value == nil || !a.machine.mayCarryReference(value.Type()) || a.machine.failed {
		return nil
	}
	key := migrationSSAQuery{c, value}
	if node := a.values[key]; node != nil {
		return node
	}
	if !a.machine.spend() {
		return nil
	}
	node := newMigrationSSAAliasNode()
	a.values[key] = node
	copyValue := func(v ssa.Value) { a.copy(a.value(c, v), node) }
	load := func(address ssa.Value) {
		if source := a.value(c, address); source != nil {
			source.loads = append(source.loads, node)
			if source.unknown {
				a.unknown(node)
			}
			for _, location := range source.points {
				a.copy(a.cell(location), node)
			}
		}
	}
	switch v := value.(type) {
	case *ssa.Const, *ssa.Function:
		// Constants and functions without captures reference no local object.
	case *ssa.Alloc:
		a.add(node, &migrationSSALocation{object: c.object(v), typ: v.Type().Underlying().(*types.Pointer).Elem()})
	case *ssa.Parameter, *ssa.FreeVar:
		if binding, ok := c.parameters[value]; ok {
			a.copy(a.value(binding.context, binding.expression), node)
		} else {
			a.unknown(node)
		}
	case *ssa.FieldAddr:
		if source := a.value(c, v.X); source != nil {
			field := migrationSSAAliasField{v.Field, v.Type().Underlying().(*types.Pointer).Elem(), node}
			source.fields = append(source.fields, field)
			if source.unknown {
				a.unknown(node)
			}
			for _, location := range source.points {
				a.add(node, location.field(field.index, field.typ))
			}
		}
	case *ssa.IndexAddr:
		if index, ok := v.Index.(*ssa.Const); ok && index.Value != nil {
			if n, ok := constant.Int64Val(index.Value); ok && n >= 0 && n < 1024 {
				if source := a.value(c, v.X); source != nil {
					field := migrationSSAAliasField{int(n), v.Type().Underlying().(*types.Pointer).Elem(), node}
					source.fields = append(source.fields, field)
					if source.unknown {
						a.unknown(node)
					}
					for _, location := range source.points {
						a.add(node, location.field(field.index, field.typ))
					}
				}
				break
			}
		}
		// Unknown indices already invalidate the backing allocation. Retain
		// its provenance so writes through the index also escape stored values.
		copyValue(v.X)
	case *ssa.UnOp:
		if v.Op == token.MUL {
			load(v.X)
		} else {
			a.unknown(node)
		}
	case *ssa.MakeInterface:
		copyValue(v.X)
	case *ssa.ChangeInterface:
		copyValue(v.X)
	case *ssa.ChangeType:
		copyValue(v.X)
	case *ssa.Convert:
		copyValue(v.X)
		a.unknown(node)
	case *ssa.MultiConvert:
		copyValue(v.X)
		a.unknown(node)
	case *ssa.TypeAssert:
		copyValue(v.X)
	case *ssa.Slice:
		copyValue(v.X)
	case *ssa.Phi:
		for _, edge := range v.Edges {
			copyValue(edge)
		}
	case *ssa.MakeClosure:
		for _, capture := range v.Bindings {
			copyValue(capture)
		}
	case *ssa.Call:
		if atomicOperation(v.Common()) == "Load" && len(v.Call.Args) > 0 {
			load(v.Call.Args[0])
		} else {
			a.returns(c, v, 0, node)
		}
	case *ssa.Extract:
		switch tuple := v.Tuple.(type) {
		case *ssa.Call:
			a.returns(c, tuple, v.Index, node)
		case *ssa.TypeAssert:
			if v.Index == 0 {
				copyValue(tuple.X)
			}
		default:
			a.unknown(node)
		}
	default:
		a.unknown(node)
	}
	return node
}

func (a *migrationSSAAliases) solve() {
	for len(a.queue) > 0 && !a.machine.failed {
		node := a.queue[0]
		a.queue = a.queue[1:]
		pending := node.pending
		node.pending, node.queued = nil, false
		if node.unknown {
			for to := range node.copies {
				a.unknown(to)
			}
			for _, field := range node.fields {
				a.unknown(field.to)
			}
			for _, to := range node.loads {
				a.unknown(to)
			}
		}
		for _, location := range pending {
			for to := range node.copies {
				a.add(to, location)
			}
			for _, field := range node.fields {
				a.add(field.to, location.field(field.index, field.typ))
			}
			for _, to := range node.loads {
				a.copy(a.cell(location), to)
			}
			for _, from := range node.stores {
				a.copy(from, a.cell(location))
			}
		}
	}
}

func (m *migrationSSAMachine) findEscapes() {
	a := m.aliases
	if a == nil {
		a = &migrationSSAAliases{
			machine: m, values: make(map[migrationSSAQuery]*migrationSSAAliasNode),
			cells: make(map[migrationSSAAliasKey]*migrationSSAAliasNode), calls: make(map[migrationSSAQuery]*migrationSSAContext),
			contained: make(map[migrationSSAAliasKey][]*migrationSSAAliasNode),
		}
		// The may-call inventory remains valid when a proven call is widened.
		// Keeping its alias constraints avoids deleting possible references.
		for _, c := range m.contexts {
			for call, child := range c.children {
				if value := call.Value(); value != nil {
					a.calls[migrationSSAQuery{c, value}] = child
				}
			}
		}
		for _, write := range m.writes {
			address := a.value(write.context, write.address)
			value := a.value(write.context, write.value)
			if value == nil {
				continue
			}
			a.stores = append(a.stores, migrationSSAAliasStore{address, value})
			if address != nil {
				address.stores = append(address.stores, value)
				for _, location := range address.points {
					a.copy(value, a.cell(location))
				}
			}
		}
		m.aliases = a
	}
	var escaped []*migrationSSAAliasNode
	for _, escape := range m.escapes {
		if node := a.value(escape.context, escape.expression); node != nil {
			escaped = append(escaped, node)
		}
	}
	a.solve()
	a.writes = make(map[*migrationSSAObject][]migrationSSAAliasWrite)
	for index, write := range m.writes {
		if address := a.values[migrationSSAQuery{write.context, write.address}]; address != nil {
			for _, location := range address.points {
				a.writes[location.object] = append(a.writes[location.object], migrationSSAAliasWrite{index, location, len(address.points) == 1 && !address.unknown})
			}
		}
	}
	m.aliases = a
	for _, store := range a.stores {
		if store.address == nil || store.address.unknown || len(store.address.points) == 0 {
			// Stores through an external or unresolved address can publish any
			// referenced local object, even when no destination is known.
			escaped = append(escaped, store.value)
		}
	}
	queue := append([]*migrationSSALocation(nil), m.escaped...)
	add := func(location *migrationSSALocation) {
		for _, old := range m.escaped {
			if old.contains(location) {
				return
			}
		}
		if m.spend() {
			m.escaped = append(m.escaped, location)
			queue = append(queue, location)
		}
	}
	for _, node := range escaped {
		for _, location := range node.points {
			add(location)
		}
	}
	for len(queue) > 0 && !m.failed {
		location := queue[0]
		queue = queue[1:]
		for _, cell := range a.contained[migrationSSAAliasKey{location.object, location.path}] {
			for _, stored := range cell.points {
				add(stored)
			}
		}
	}
}

// Discovery may resolve a receiver before all mutations and escapes are known.
// Widen unstable calls to their conservative effects and invalidate their inputs.
// Retaining the old inventory overapproximates writes; it never invents purity.
func (m *migrationSSAMachine) stabilizeEscapes() {
	for !m.failed {
		m.phase = "alias propagation"
		m.findEscapes()
		m.phase = "call stabilization"
		clear(m.addresses)
		changed := false
		for _, c := range m.contexts {
			for _, block := range c.function.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok || c.children[call] == nil {
						continue
					}
					actual, _, _ := c.resolve(call, make(map[migrationSSAQuery]bool))
					if actual != c.children[call].function {
						delete(c.children, call)
						clear(m.addresses)
						m.escapeCall(c, call, nil)
						m.cuts[call] = "call target depends on invalidated state; its inputs may be modified and conservative effects are retained"
						changed = true
					}
				}
			}
		}
		if !changed {
			return
		}
		clear(m.addresses)
	}
}
