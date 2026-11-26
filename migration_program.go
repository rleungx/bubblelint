package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// Decision summaries retain simple branches and callback parameters instead of
// merging their effects. Unsupported bodies retain the ordinary effect summary.
// The representation contains no AST or types.Objects, so facts can serialize it.
type migrationProgram struct {
	Steps []migrationInstruction
	Size  int
}

type migrationInstruction struct {
	Kind  byte // e: expression, i: if, r: return, a: immutable binding
	Expr  *migrationExpression
	Then  []migrationInstruction
	Else  []migrationInstruction
	Slots []int `json:",omitempty"`
}

type migrationExpression struct {
	Kind          byte // ?: unknown, b: bool, n: nil, p: bound value, a: comma-ok type assertion, f: field, c: call, v: function, o: object, !/&/|/=/~/@: operators
	Text          string
	Input         int // -1 is the receiver
	Left          *migrationExpression
	Right         *migrationExpression
	Args          []*migrationExpression
	Target        *migrationTarget
	Fields        []migrationProgramField
	Discard       bool
	Type          *migrationType `json:",omitempty"`
	Assert        *migrationType `json:",omitempty"`
	Method        string         `json:",omitempty"`
	MethodPackage string         `json:",omitempty"`
	Box           bool           `json:",omitempty"`
	Concrete      bool           `json:",omitempty"`
}

type migrationProgramField struct {
	Name  string
	Value *migrationExpression
}

type migrationTarget struct {
	Frame            migrationFrame
	Program          *migrationProgram
	Reasons          []migrationReason
	Timer            bool
	Callbacks        []int
	OptionalCallback bool
	Formatting       bool `json:",omitempty"`
}

type migrationProgramBuilder struct {
	c      *checker
	inputs map[types.Object]int
	left   *int
}

func (c *checker) migrationProgram(fn *types.Func, body function, budget *int) *migrationProgram {
	if program, known := c.programs[body.body]; known {
		if program != nil && program.Size <= *budget {
			*budget -= program.Size
			return program
		}
		return nil
	}
	if c.programBuilding[body.body] || *budget <= 0 {
		return nil
	}
	if fn != nil {
		sig := fn.Type().(*types.Signature)
		if sig.TypeParams().Len() != 0 || sig.RecvTypeParams().Len() != 0 {
			// Decision facts do not carry a type-argument environment. Keep
			// the ordinary effect summary rather than interpreting constraints
			// as the runtime types of parameter values.
			c.programs[body.body] = nil
			return nil
		}
	}
	c.programBuilding[body.body] = true
	defer delete(c.programBuilding, body.body)
	inputs := make(map[types.Object]int)
	if fn != nil {
		sig := fn.Type().(*types.Signature)
		if sig.Recv() != nil && !c.assigned[sig.Recv()] {
			inputs[sig.Recv()] = -1
		}
		for i := 0; i < sig.Params().Len(); i++ {
			param := sig.Params().At(i)
			if !c.assigned[param] {
				inputs[param] = i
			}
		}
	} else if body.params != nil {
		i := 0
		for _, field := range body.params.List {
			for _, name := range field.Names {
				obj := c.pass.TypesInfo.Defs[name]
				if !c.assigned[obj] {
					inputs[obj] = i
				}
				i++
			}
		}
	}
	before := *budget
	builder := migrationProgramBuilder{c, inputs, budget}
	steps, ok := builder.statements(body.body.List)
	var program *migrationProgram
	if ok {
		program = &migrationProgram{Steps: steps, Size: before - *budget}
	}
	if program != nil || before >= 128 || *budget >= 0 {
		c.programs[body.body] = program
	}
	return program
}

func (b migrationProgramBuilder) statements(statements []ast.Stmt) ([]migrationInstruction, bool) {
	var result []migrationInstruction
	for _, stmt := range statements {
		*b.left--
		if *b.left < 0 {
			return nil, false
		}
		var op migrationInstruction
		switch stmt := stmt.(type) {
		case *ast.EmptyStmt:
			continue
		case *ast.ExprStmt:
			op.Kind, op.Expr = 'e', b.expression(stmt.X)
		case *ast.AssignStmt:
			var ok bool
			op, ok = b.binding(stmt.Lhs, stmt.Rhs)
			if !ok {
				return nil, false
			}
		case *ast.DeclStmt:
			decl, ok := stmt.Decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.VAR || len(decl.Specs) != 1 {
				return nil, false
			}
			spec := decl.Specs[0].(*ast.ValueSpec)
			var lhs []ast.Expr
			for _, name := range spec.Names {
				lhs = append(lhs, name)
			}
			op, ok = b.binding(lhs, spec.Values)
			if !ok {
				return nil, false
			}
		case *ast.IfStmt:
			if stmt.Init != nil {
				initial, ok := b.statements([]ast.Stmt{stmt.Init})
				if !ok {
					return nil, false
				}
				result = append(result, initial...)
			}
			op.Kind, op.Expr = 'i', b.expression(stmt.Cond)
			var ok bool
			if op.Then, ok = b.statements(stmt.Body.List); !ok {
				return nil, false
			}
			if stmt.Else != nil {
				other := []ast.Stmt{stmt.Else}
				if block, ok := stmt.Else.(*ast.BlockStmt); ok {
					other = block.List
				}
				if op.Else, ok = b.statements(other); !ok {
					return nil, false
				}
			}
		case *ast.ReturnStmt:
			if len(stmt.Results) > 1 {
				return nil, false
			}
			op.Kind = 'r'
			if len(stmt.Results) == 1 {
				op.Expr = b.expression(stmt.Results[0])
			}
		default:
			return nil, false
		}
		if op.Expr == nil && op.Kind != 'r' || op.Expr != nil && !validMigrationExpression(op.Expr) || *b.left < 0 {
			return nil, false
		}
		result = append(result, op)
	}
	return result, true
}

func (b migrationProgramBuilder) binding(lhs, rhs []ast.Expr) (migrationInstruction, bool) {
	op := migrationInstruction{Kind: 'a'}
	if len(rhs) != 1 || len(lhs) == 0 || len(lhs) > 2 {
		return op, false
	}
	if len(lhs) == 2 {
		assertion, ok := ast.Unparen(rhs[0]).(*ast.TypeAssertExpr)
		if !ok || assertion.Type == nil {
			return op, false
		}
		want := describeMigrationType(b.c.pass.TypesInfo.TypeOf(assertion.Type), b.left)
		if want == nil {
			return op, false
		}
		op.Expr = &migrationExpression{Kind: 'a', Left: b.expression(assertion.X), Assert: want}
	} else {
		op.Expr = b.expression(rhs[0])
	}
	for _, expr := range lhs {
		id, ok := expr.(*ast.Ident)
		if !ok {
			return op, false
		}
		obj := b.c.pass.TypesInfo.ObjectOf(id)
		if id.Name == "_" {
			op.Slots = append(op.Slots, 0)
			continue
		}
		if obj == nil || b.c.assignments[obj] != 1 {
			return op, false
		}
		if _, exists := b.inputs[obj]; exists {
			return op, false
		}
		slot := -len(b.inputs) - 2
		b.inputs[obj] = slot
		op.Slots = append(op.Slots, slot)
	}
	return op, true
}

func (b migrationProgramBuilder) expression(expr ast.Expr) (result *migrationExpression) {
	*b.left--
	if *b.left < 0 {
		return nil
	}
	if expr == nil {
		return &migrationExpression{Kind: '?'}
	}
	typ := b.c.pass.TypesInfo.TypeOf(expr)
	if typ != nil {
		if _, parameter := types.Unalias(typ).(*types.TypeParam); parameter {
			return nil
		}
		if _, iface := typ.Underlying().(*types.Interface); iface {
			defer func() {
				if result != nil {
					result.Box = true
				}
			}()
		} else {
			defer func() {
				if result != nil {
					if basic, ok := typ.Underlying().(*types.Basic); ok && basic.Info()&types.IsUntyped != 0 {
						return
					}
					result.Concrete = true
					left := *b.left
					if desc := describeMigrationType(typ, &left); desc != nil && left >= 0 {
						result.Type, *b.left = desc, left
					}
				}
			}()
		}
	}
	expr = ast.Unparen(expr)
	if id, ok := expr.(*ast.Ident); ok {
		obj := b.c.pass.TypesInfo.ObjectOf(id)
		if index, ok := b.inputs[obj]; ok {
			return &migrationExpression{Kind: 'p', Input: index}
		}
		if variable, ok := obj.(*types.Var); ok && variable.Pkg() != nil && variable.Parent() == variable.Pkg().Scope() {
			return nil
		}
		if variable, ok := obj.(*types.Var); ok && variable.Parent() != nil {
			defer func() {
				if *b.left >= 0 && (result == nil || !validMigrationExpression(result)) {
					// Reading a local does not execute its initializer again.
					// Unsupported value provenance remains unknown; effects of
					// the initializer are checked where it executes.
					result = &migrationExpression{Kind: '?'}
				}
			}()
		}
	}
	if variable, ok := directObject(b.c.pass.TypesInfo, expr).(*types.Var); ok && variable.Pkg() != nil && variable.Parent() == variable.Pkg().Scope() {
		return nil
	}
	expr = b.c.resolve(expr)
	value := b.c.pass.TypesInfo.Types[expr]
	if value.IsNil() {
		return &migrationExpression{Kind: 'n'}
	}
	if value.Value != nil && value.Value.Kind() == constant.Bool {
		return &migrationExpression{Kind: 'b', Text: value.Value.ExactString()}
	}
	if value.Value != nil {
		return &migrationExpression{Kind: '?'}
	}
	switch expr := expr.(type) {
	case *ast.Ident:
		if fn := b.c.object(expr); fn != nil {
			return &migrationExpression{Kind: 'v', Target: b.target(fn, expr, nil)}
		}
		return &migrationExpression{Kind: '?'}
	case *ast.UnaryExpr:
		if expr.Op == token.NOT {
			return &migrationExpression{Kind: '!', Left: b.expression(expr.X)}
		}
		if expr.Op == token.AND {
			return &migrationExpression{Kind: '@', Left: b.expression(expr.X)}
		}
		return nil
	case *ast.BinaryExpr:
		kind := map[token.Token]byte{token.LAND: '&', token.LOR: '|', token.EQL: '=', token.NEQ: '~'}[expr.Op]
		if kind == 0 {
			return nil
		}
		return &migrationExpression{Kind: kind, Left: b.expression(expr.X), Right: b.expression(expr.Y)}
	case *ast.SelectorExpr:
		if fn := b.c.object(expr); fn != nil && fn.Type().(*types.Signature).Recv() == nil {
			return &migrationExpression{Kind: 'v', Target: b.target(fn, expr, nil)}
		}
		return &migrationExpression{Kind: 'f', Text: expr.Sel.Name, Left: b.expression(expr.X)}
	case *ast.CallExpr:
		result := &migrationExpression{Kind: 'c', Discard: b.c.objectReferences[expr] > 1}
		if builtin, ok := directObject(b.c.pass.TypesInfo, expr.Fun).(*types.Builtin); ok {
			switch builtin.Name() {
			case "close", "len", "cap":
				result.Kind = 'k'
			default:
				return nil
			}
		}
		fn := b.c.migrationCallTarget(expr)
		args := expr.Args
		if fn != nil {
			result.Target = b.target(fn, expr, expr)
			if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
				if _, iface := recv.Type().Underlying().(*types.Interface); iface {
					result.Method = fn.Name()
					if fn.Pkg() != nil {
						result.MethodPackage = fn.Pkg().Path()
					}
				}
			}
			if _, receiver, methodArgs := b.c.methodCall(expr); receiver != nil {
				result.Left, args = b.expression(receiver), methodArgs
				if result.Left == nil {
					return nil
				}
			}
		} else if result.Kind != 'k' {
			result.Left = b.expression(expr.Fun)
		}
		for _, arg := range args {
			result.Args = append(result.Args, b.expression(arg))
		}
		return result
	case *ast.FuncLit:
		body := function{expr.Body, expr.Type.Params}
		return &migrationExpression{Kind: 'v', Target: &migrationTarget{
			Frame:   migrationFrame{Symbol: "func literal", Position: b.c.pass.Fset.PositionFor(expr.Pos(), false)},
			Program: b.c.migrationProgram(nil, body, b.left),
			Reasons: b.c.coarseEffects.reasons[expr.Body].ordered(), Timer: b.c.coarseEffects.timers[expr.Body],
		}}
	case *ast.CompositeLit:
		structure, ok := b.c.pass.TypesInfo.TypeOf(expr).Underlying().(*types.Struct)
		if !ok || b.c.objectReferences[expr] > 1 {
			return nil
		}
		fields := make(map[string]*migrationExpression)
		for i := 0; i < structure.NumFields(); i++ {
			field := structure.Field(i)
			if basic, ok := field.Type().Underlying().(*types.Basic); ok && basic.Kind() == types.Bool {
				fields[field.Name()] = &migrationExpression{Kind: 'b', Text: "false"}
			} else {
				kind := byte('?')
				switch field.Type().Underlying().(type) {
				case *types.Pointer, *types.Interface, *types.Map, *types.Slice, *types.Chan, *types.Signature:
					kind = 'n'
				}
				fields[field.Name()] = &migrationExpression{Kind: kind}
			}
		}
		result := &migrationExpression{Kind: 'o'}
		for _, item := range expr.Elts {
			pair, ok := item.(*ast.KeyValueExpr)
			if !ok {
				return nil
			}
			name, ok := pair.Key.(*ast.Ident)
			if !ok {
				return nil
			}
			// Calls in field initializers execute in the literal's written order.
			result.Fields = append(result.Fields, migrationProgramField{Name: name.Name, Value: b.expression(pair.Value)})
			delete(fields, name.Name)
		}
		for i := 0; i < structure.NumFields(); i++ {
			name := structure.Field(i).Name()
			if value, exists := fields[name]; exists {
				result.Fields = append(result.Fields, migrationProgramField{Name: name, Value: value})
			}
		}
		return result
	default:
		return nil
	}
}

func (b migrationProgramBuilder) target(fn *types.Func, site ast.Node, call *ast.CallExpr) *migrationTarget {
	target := &migrationTarget{Frame: migrationFrame{Symbol: fn.FullName(), Position: b.c.pass.Fset.PositionFor(site.Pos(), false)}}
	target.Formatting = migrationFormattingOperation(fn)
	if fn.Pkg() != nil {
		switch fn.Pkg().Path() {
		case "testing":
			if fn.Name() == "Cleanup" {
				target.Callbacks = []int{0}
			}
			if fn.Name() == "Run" {
				target.Callbacks = []int{1}
			}
		case "time":
			if fn.Name() == "AfterFunc" {
				target.Callbacks, target.OptionalCallback = []int{1}, true
			}
		case "sync":
			if fn.Name() == "Go" {
				target.Callbacks = []int{0}
			}
		}
	}
	if local, ok := b.c.functions[fn.Origin()]; ok {
		target.Program = b.c.migrationProgram(fn, local, b.left)
		target.Reasons = b.c.coarseEffects.reasons[local.body].ordered()
		target.Timer = b.c.coarseEffects.timers[local.body]
		return target
	}
	if call == nil {
		call = &ast.CallExpr{Fun: site.(ast.Expr)}
	}
	// Reuse the operation models and opaque-call policy without modifying the
	// summaries currently being built.
	temporary := *b.c
	temporary.effects = &migrationEffectsResult{reasons: make(map[*ast.BlockStmt]migrationReasonSet), imported: b.c.coarseEffects.imported}
	temporary.effectConditional = false
	body := &ast.BlockStmt{}
	fact := temporary.migrationInvocationEffects(body, call, fn)
	if fact != nil {
		target.Timer = fact.Timer
		if fact.program != nil && fact.program.Size <= *b.left {
			target.Program = fact.program
			*b.left -= fact.program.Size
		}
		// Invocation adds the source frame to the callee's fallback evidence.
		target.Reasons = fact.Reasons
	} else {
		target.Reasons = temporary.effects.reasons[body].ordered()
		for i := range target.Reasons {
			if trace := target.Reasons[i].Trace; len(trace) != 0 && trace[0].Symbol == target.Frame.Symbol {
				target.Reasons[i].Trace = trace[1:]
			}
		}
		target.Timer = migrationTimerOperation(fn)
	}
	return target
}

func (c *checker) prepareMigrationPrograms() {
	c.programs = make(map[*ast.BlockStmt]*migrationProgram)
	c.programBuilding = make(map[*ast.BlockStmt]bool)
	c.objectReferences = make(map[ast.Expr]int)
	for _, file := range c.pass.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			id, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			if _, ok := c.pass.TypesInfo.Uses[id].(*types.Var); !ok {
				return true
			}
			value := c.resolve(id)
			if pointer, ok := value.(*ast.UnaryExpr); ok && pointer.Op == token.AND {
				value = ast.Unparen(pointer.X)
			}
			switch value.(type) {
			case *ast.CompositeLit, *ast.CallExpr:
				c.objectReferences[value]++
			}
			return true
		})
	}
}

func validMigrationExpression(expr *migrationExpression) bool {
	if expr == nil {
		return false
	}
	switch expr.Kind {
	case '!', 'f', 'a', '@':
		if !validMigrationExpression(expr.Left) {
			return false
		}
	case '&', '|', '=', '~':
		if !validMigrationExpression(expr.Left) || !validMigrationExpression(expr.Right) {
			return false
		}
	case 'c':
		if expr.Target == nil && !validMigrationExpression(expr.Left) {
			return false
		}
		if expr.Left != nil && !validMigrationExpression(expr.Left) {
			return false
		}
	}
	for _, arg := range expr.Args {
		if !validMigrationExpression(arg) {
			return false
		}
	}
	for _, field := range expr.Fields {
		if !validMigrationExpression(field.Value) {
			return false
		}
	}
	return true
}
