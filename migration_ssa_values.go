package bubblelint

import (
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type migrationSSAValue struct {
	constant                             constant.Value
	nilKnown, isNil                      bool
	location                             *migrationSSALocation
	typ                                  types.Type
	function                             *ssa.Function
	closure                              *migrationSSAContext
	closureSite                          ssa.Instruction
	bindings                             []ssa.Value
	actual                               *migrationSSAValue
	tuple                                []migrationSSAValue
	choices                              []migrationSSAValue
	ownedChannel                         bool
	contextKnown, contextHasDone, cancel bool
	bubbleTestingT                       bool
}

type migrationSSAObject struct {
	allocation *ssa.Alloc
	context    *migrationSSAContext
}

type migrationSSALocation struct {
	object *migrationSSAObject
	path   string
	typ    types.Type
}

func (l *migrationSSALocation) field(index int, typ types.Type) *migrationSSALocation {
	if l == nil {
		return nil
	}
	return &migrationSSALocation{object: l.object, path: l.path + strconv.Itoa(index) + "/", typ: typ}
}

func (l *migrationSSALocation) contains(other *migrationSSALocation) bool {
	return l != nil && other != nil && l.object == other.object && strings.HasPrefix(other.path, l.path)
}

func migrationSSAEqual(a, b migrationSSAValue) bool {
	if a.constant != nil && b.constant != nil {
		return a.constant.Kind() == b.constant.Kind() && a.constant.ExactString() == b.constant.ExactString()
	}
	if a.location != nil && b.location != nil {
		return a.location.object == b.location.object && a.location.path == b.location.path && types.Identical(a.typ, b.typ)
	}
	if a.nilKnown && b.nilKnown && a.isNil && b.isNil {
		return true
	}
	if a.function != nil && b.function == a.function && a.closure == b.closure {
		return true
	}
	return false
}

func migrationSSAJoin(values []migrationSSAValue) migrationSSAValue {
	if len(values) == 0 {
		return migrationSSAValue{}
	}
	result := values[0]
	for _, value := range values[1:] {
		if !migrationSSAEqual(result, value) {
			return migrationSSAValue{choices: values}
		}
	}
	return result
}

func migrationSSABoolean(v migrationSSAValue) (bool, bool) {
	if v.constant != nil && v.constant.Kind() == constant.Bool {
		return constant.BoolVal(v.constant), true
	}
	return false, false
}

func migrationSSAZero(typ types.Type) migrationSSAValue {
	if typ == nil {
		return migrationSSAValue{}
	}
	switch t := typ.Underlying().(type) {
	case *types.Pointer, *types.Interface, *types.Signature, *types.Chan, *types.Map, *types.Slice:
		return migrationSSAValue{nilKnown: true, isNil: true, typ: typ}
	case *types.Basic:
		switch {
		case t.Info()&types.IsBoolean != 0:
			return migrationSSAValue{constant: constant.MakeBool(false), typ: typ}
		case t.Info()&types.IsString != 0:
			return migrationSSAValue{constant: constant.MakeString(""), typ: typ}
		case t.Info()&types.IsNumeric != 0:
			return migrationSSAValue{constant: constant.MakeInt64(0), typ: typ}
		}
	}
	return migrationSSAValue{}
}

type migrationSSAQuery struct {
	context *migrationSSAContext
	value   ssa.Value
}

func (c *migrationSSAContext) value(v ssa.Value, at ssa.Instruction, seen map[migrationSSAQuery]bool) (result migrationSSAValue) {
	if v == nil {
		return migrationSSAValue{}
	}
	key := migrationSSAQuery{c, v}
	if address, ok := c.machine.addresses[key]; ok {
		return address
	}
	if !c.machine.spend() {
		return migrationSSAValue{}
	}
	if seen[key] {
		return migrationSSAValue{}
	}
	seen[key] = true
	reads := c.machine.stateReads
	defer func() {
		delete(seen, key)
		// Allocation addresses are immutable. Loads and control-dependent
		// queries must be reevaluated as mutation and escape facts change.
		if result.location != nil && reads == c.machine.stateReads && !c.machine.failed {
			c.machine.addresses[key] = result
		}
	}()
	result = migrationSSAValue{typ: v.Type()}
	switch v := v.(type) {
	case *ssa.Const:
		result.constant = v.Value
		result.nilKnown = v.IsNil()
		result.isNil = v.IsNil()
	case *ssa.Parameter:
		if binding, ok := c.parameters[v]; ok {
			return binding.value(seen)
		}
		result.bubbleTestingT = c.parent == nil && isTestingT(v.Type())
	case *ssa.FreeVar:
		if binding, ok := c.parameters[v]; ok {
			return binding.value(seen)
		}
	case *ssa.Alloc:
		object := c.objects[v]
		if object == nil {
			object = &migrationSSAObject{v, c}
			c.objects[v] = object
		}
		result.location = &migrationSSALocation{object: object, typ: v.Type().Underlying().(*types.Pointer).Elem()}
		result.nilKnown = true
	case *ssa.FieldAddr:
		base := c.value(v.X, at, seen)
		result.bubbleTestingT = base.bubbleTestingT
		result.location = base.location.field(v.Field, v.Type().Underlying().(*types.Pointer).Elem())
		result.nilKnown = result.location != nil
		if len(base.choices) > 0 {
			for _, choice := range base.choices {
				location := choice.location.field(v.Field, v.Type().Underlying().(*types.Pointer).Elem())
				result.choices = append(result.choices, migrationSSAValue{location: location, typ: v.Type()})
			}
		}
	case *ssa.IndexAddr:
		base, index := c.value(v.X, at, seen), c.value(v.Index, at, seen)
		if index.constant != nil && index.constant.Kind() == constant.Int {
			if i, ok := constant.Int64Val(index.constant); ok && i >= 0 && i < 1024 {
				result.location = base.location.field(int(i), v.Type().Underlying().(*types.Pointer).Elem())
				result.nilKnown = result.location != nil
			}
		}
	case *ssa.UnOp:
		switch v.Op {
		case token.MUL:
			if _, aggregate := v.Type().Underlying().(*types.Struct); aggregate {
				break
			}
			return c.machine.read(c.value(v.X, v, seen).location, c, v, v.Type(), false, seen)
		case token.NOT:
			if value, known := migrationSSABoolean(c.value(v.X, v, seen)); known {
				result.constant = constant.MakeBool(!value)
			}
		}
	case *ssa.MakeInterface:
		actual := c.value(v.X, at, seen)
		result.actual = &actual
		result.typ = v.X.Type()
		result.nilKnown = true
	case *ssa.ChangeInterface:
		return c.value(v.X, at, seen)
	case *ssa.ChangeType:
		result = c.value(v.X, at, seen)
		result.typ = v.Type()
	case *ssa.TypeAssert:
		value := c.value(v.X, at, seen)
		if value.actual != nil && types.Identical(value.actual.typ, v.AssertedType) {
			if v.CommaOk {
				return migrationSSAValue{tuple: []migrationSSAValue{*value.actual, {constant: constant.MakeBool(true)}}}
			}
			return *value.actual
		}
	case *ssa.Function:
		result.function = v
	case *ssa.MakeClosure:
		result.function = v.Fn.(*ssa.Function)
		result.closure = c
		result.closureSite = v
		result.bindings = v.Bindings
	case *ssa.MakeChan:
		result.ownedChannel = true
		result.nilKnown = true
	case *ssa.Slice:
		// Keep the result unknown until backing-array offsets are modeled.
	case *ssa.Call:
		return c.callValue(v, seen)
	case *ssa.Extract:
		if selection, ok := v.Tuple.(*ssa.Select); ok && v.Index == 0 {
			var choices []migrationSSAValue
			for i, state := range selection.States {
				channel := c.value(state.Chan, selection, seen)
				if !channel.nilKnown || !channel.isNil {
					choices = append(choices, migrationSSAValue{constant: constant.MakeInt64(int64(i))})
				}
			}
			if !selection.Blocking {
				choices = append(choices, migrationSSAValue{constant: constant.MakeInt64(-1)})
			}
			choices = c.constrain(v, choices, at.Block())
			return migrationSSAJoin(choices)
		}
		tuple := c.value(v.Tuple, at, seen)
		if v.Index < len(tuple.tuple) {
			return tuple.tuple[v.Index]
		}
	case *ssa.Phi:
		var values []migrationSSAValue
		for _, edge := range v.Edges {
			values = append(values, c.value(edge, at, seen))
		}
		return migrationSSAJoin(values)
	case *ssa.BinOp:
		return migrationSSABinary(v.Op, c.value(v.X, v, seen), c.value(v.Y, v, seen))
	}
	return result
}

func migrationSSABinary(op token.Token, a, b migrationSSAValue) migrationSSAValue {
	if len(a.choices) > 0 || len(b.choices) > 0 {
		left, right := a.choices, b.choices
		if len(left) == 0 {
			left = []migrationSSAValue{a}
		}
		if len(right) == 0 {
			right = []migrationSSAValue{b}
		}
		if len(left)*len(right) > 16 {
			return migrationSSAValue{}
		}
		var results []migrationSSAValue
		for _, x := range left {
			for _, y := range right {
				results = append(results, migrationSSABinary(op, x, y))
			}
		}
		return migrationSSAJoin(results)
	}
	if op == token.EQL || op == token.NEQ {
		if a.nilKnown && b.nilKnown && (a.isNil || b.isNil) {
			return migrationSSAValue{constant: constant.MakeBool((a.isNil == b.isNil) == (op == token.EQL))}
		}
	}
	if a.constant != nil && b.constant != nil {
		switch op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return migrationSSAValue{constant: constant.MakeBool(constant.Compare(a.constant, op, b.constant))}
		}
	}
	return migrationSSAValue{}
}

// Select indices have a finite range. Dominating case comparisons exclude
// impossible successors without simulating channel readiness or scheduling.
func (c *migrationSSAContext) constrain(value ssa.Value, choices []migrationSSAValue, block *ssa.BasicBlock) []migrationSSAValue {
	c.machine.stateReads++
	for _, predecessor := range c.function.Blocks {
		if predecessor == block || !predecessor.Dominates(block) {
			continue
		}
		branch, ok := predecessor.Instrs[len(predecessor.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || comparison.Op != token.EQL && comparison.Op != token.NEQ {
			continue
		}
		var literal *ssa.Const
		if comparison.X == value {
			literal, _ = comparison.Y.(*ssa.Const)
		} else if comparison.Y == value {
			literal, _ = comparison.X.(*ssa.Const)
		}
		if literal == nil || literal.Value == nil {
			continue
		}
		taken := predecessor.Succs[0].Dominates(block)
		if !taken && !predecessor.Succs[1].Dominates(block) {
			continue
		}
		retained := make([]migrationSSAValue, 0, len(choices))
		for _, candidate := range choices {
			truth, known := migrationSSABoolean(migrationSSABinary(comparison.Op, candidate, migrationSSAValue{constant: literal.Value}))
			if !known || truth == taken {
				retained = append(retained, candidate)
			}
		}
		choices = retained
	}
	return choices
}
