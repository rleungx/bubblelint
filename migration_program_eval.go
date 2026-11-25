package bubblelint

import (
	"go/ast"
	"go/types"
)

type migrationValue struct {
	kind   byte
	text   string
	target *migrationTarget
	fields map[string]migrationValue
}

func invalidateMigrationFields(value migrationValue) {
	for name, field := range value.fields {
		delete(value.fields, name)
		invalidateMigrationFields(field)
	}
}

type migrationEvaluation struct {
	inputs      map[int]migrationValue
	reasons     migrationReasonSet
	timer       bool
	conditional bool
	left        *int
	frame       migrationFrame
	mayReturn   bool
}

func (s *migrationEvaluation) reason(reason migrationReason) {
	reason.Conditional = reason.Conditional || s.conditional
	s.reasons.add(reason)
}

func (s *migrationEvaluation) expression(expr *migrationExpression) migrationValue {
	*s.left--
	if *s.left < 0 {
		s.reason(migrationReason{Kind: effectOpaqueCall, Message: "context analysis exceeded its work limit", Trace: []migrationFrame{s.frame}})
		return migrationValue{}
	}
	if expr == nil {
		return migrationValue{}
	}
	switch expr.Kind {
	case 'b', 'n':
		return migrationValue{kind: expr.Kind, text: expr.Text}
	case 'p':
		return s.inputs[expr.Input]
	case 'v':
		return migrationValue{kind: 'v', target: expr.Target}
	case 'o':
		value := migrationValue{kind: 'o', fields: make(map[string]migrationValue)}
		for _, field := range expr.Fields {
			value.fields[field.Name] = s.expression(field.Value)
		}
		return value
	case 'f':
		value := s.expression(expr.Left)
		return value.fields[expr.Text]
	case '!':
		value := s.expression(expr.Left)
		if value.kind == 'b' {
			return migrationValue{kind: 'b', text: boolText(value.text != "true")}
		}
	case '&', '|', '=', '~':
		left := s.expression(expr.Left)
		if left.kind == 'b' && (expr.Kind == '&' && left.text == "false" || expr.Kind == '|' && left.text == "true") {
			return left
		}
		previous := s.conditional
		if (expr.Kind == '&' || expr.Kind == '|') && left.kind != 'b' {
			s.conditional = true
		}
		right := s.expression(expr.Right)
		s.conditional = previous
		if expr.Kind == '&' || expr.Kind == '|' {
			if left.kind == 'b' && right.kind == 'b' {
				return right
			}
			return migrationValue{}
		}
		known, equal := false, false
		if left.kind == 'b' && right.kind == 'b' {
			known, equal = true, left.text == right.text
		}
		if left.kind == 'n' && right.kind == 'n' {
			known, equal = true, true
		}
		if left.kind == 'o' && right.kind == 'n' || right.kind == 'o' && left.kind == 'n' {
			known, equal = true, false
		}
		if known {
			return migrationValue{kind: 'b', text: boolText(equal == (expr.Kind == '='))}
		}
	case 'c', 'k':
		callee := s.expression(expr.Left)
		args := make([]migrationValue, 0, len(expr.Args))
		for _, arg := range expr.Args {
			args = append(args, s.expression(arg))
		}
		if expr.Kind == 'k' {
			return migrationValue{}
		}
		target, receiver := expr.Target, callee
		if target == nil && callee.kind == 'v' {
			target, receiver = callee.target, migrationValue{}
		}
		if target == nil {
			s.reason(migrationReason{Kind: effectOpaqueCall, Message: "the callback target remains unknown for these arguments", Trace: []migrationFrame{s.frame}})
			return migrationValue{}
		}
		value := s.invoke(target, receiver, args)
		if expr.Discard {
			return migrationValue{}
		}
		return value
	}
	return migrationValue{}
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (s *migrationEvaluation) statements(steps []migrationInstruction) (migrationValue, bool) {
	for _, step := range steps {
		value := s.expression(step.Expr)
		switch step.Kind {
		case 'r':
			s.mayReturn = true
			return value, true
		case 'i':
			if value.kind == 'b' {
				branch := step.Then
				if value.text != "true" {
					branch = step.Else
				}
				if result, returned := s.statements(branch); returned {
					return result, true
				}
			} else {
				previous := s.conditional
				for _, input := range s.inputs {
					invalidateMigrationFields(input)
				}
				thenState, elseState := *s, *s
				thenState.conditional, elseState.conditional = true, true
				thenState.mayReturn, elseState.mayReturn = false, false
				thenValue, thenReturns := thenState.statements(step.Then)
				elseValue, elseReturns := elseState.statements(step.Else)
				s.timer = s.timer || thenState.timer || elseState.timer
				s.mayReturn = s.mayReturn || thenState.mayReturn || elseState.mayReturn
				s.conditional = previous || thenReturns != elseReturns || thenState.mayReturn && !thenReturns || elseState.mayReturn && !elseReturns
				if thenReturns && elseReturns {
					if thenValue.kind == elseValue.kind && thenValue.text == elseValue.text && (thenValue.kind == 'b' || thenValue.kind == 'n') {
						return thenValue, true
					}
					return migrationValue{}, true
				}
			}
		}
	}
	return migrationValue{}, false
}

func (s *migrationEvaluation) invoke(target *migrationTarget, receiver migrationValue, args []migrationValue) migrationValue {
	if target.Program == nil || *s.left <= 0 {
		for _, reason := range target.Reasons {
			reason.Trace = append([]migrationFrame{target.Frame}, reason.Trace...)
			s.reason(reason)
		}
		s.timer = s.timer || target.Timer
		previous := s.conditional
		s.conditional = previous || target.OptionalCallback
		for _, index := range target.Callbacks {
			if index < len(args) && args[index].target != nil {
				s.invoke(args[index].target, migrationValue{}, nil)
			} else {
				s.reason(migrationReason{Kind: effectOpaqueCall, Message: "the registered callback target is unknown", Trace: []migrationFrame{target.Frame}})
			}
		}
		s.conditional = previous
		// An unavailable body may mutate an object passed to it. Its old field
		// values cannot justify pruning subsequent branches.
		invalidateMigrationFields(receiver)
		for _, arg := range args {
			invalidateMigrationFields(arg)
		}
		if *s.left <= 0 {
			s.expression(nil)
		}
		return migrationValue{}
	}
	inputs := map[int]migrationValue{-1: receiver}
	for i, arg := range args {
		inputs[i] = arg
	}
	child := migrationEvaluation{inputs: inputs, reasons: make(migrationReasonSet), left: s.left, frame: target.Frame}
	value, _ := child.statements(target.Program.Steps)
	s.timer = s.timer || child.timer
	for _, reason := range child.reasons.ordered() {
		reason.Trace = append([]migrationFrame{target.Frame}, reason.Trace...)
		s.reason(reason)
	}
	return value
}

func (c *checker) specializeMigrationCall(body *ast.BlockStmt, call *ast.CallExpr, synchronous bool) (bool, bool) {
	if c.coarseEffects == nil {
		return false, false
	}
	budget := 128
	builder := migrationProgramBuilder{c: c, inputs: make(map[types.Object]int), left: &budget}
	expr := builder.expression(call)
	if expr == nil || expr.Target == nil || expr.Target.Program == nil || !validMigrationExpression(expr) {
		return false, false
	}
	work := 256
	evaluation := migrationEvaluation{inputs: make(map[int]migrationValue), reasons: make(migrationReasonSet), left: &work, frame: expr.Target.Frame}
	evaluation.expression(expr)
	if c.effects.reasons[body] == nil {
		c.effects.reasons[body] = make(migrationReasonSet)
	}
	clock := false
	for _, reason := range evaluation.reasons.ordered() {
		clock = clock || reason.Kind == effectClock
		reason.Conditional = reason.Conditional || c.effectConditional
		c.effects.reasons[body].add(reason)
	}
	if synchronous {
		c.effects.timers[body] = c.effects.timers[body] || evaluation.timer
	}
	return true, clock
}
