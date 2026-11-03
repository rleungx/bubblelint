package bubblelint

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"go/version"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Migration deliberately recognizes a small protocol instead of inferring
// arbitrary helper effects or goroutine lifetimes: each finite worker signals
// its own local channel as its last action, and the test joins every worker
// before any assertions or control flow. A timeout select may join the final
// worker only when its signal is guaranteed to precede the timeout in virtual
// time. This prevents a root from exiting while a worker is still sleeping.
type migration struct {
	c          *checker
	test       types.Object
	values     map[types.Object]bool
	channels   map[types.Object]*migrationChannel
	workers    int
	started    int
	joined     int
	sleeps     []*ast.CallExpr
	timeouts   []*ast.CallExpr
	timerTotal constant.Value
}

type migrationChannel struct {
	started bool
	joined  bool
	delay   constant.Value
}

func (c *checker) findMigrationFixes() {
	if !c.supportsSynctest() {
		return
	}
	c.migrationFixes = make(map[*ast.CallExpr][]analysis.SuggestedFix)
	referenced := make(map[types.Object]bool)
	for _, obj := range c.pass.TypesInfo.Uses {
		if _, ok := obj.(*types.Func); ok {
			referenced[obj] = true
		}
	}
	for _, file := range c.pass.Files {
		if ast.IsGenerated(file) || hasLineDirective(file) || (c.pass.TypesInfo.FileVersions[file] != "" && version.Compare(c.pass.TypesInfo.FileVersions[file], "go1.25") < 0) {
			continue
		}
		qualifier, imports := c.migrationImport(file)
		if qualifier == "" {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil || !c.isTest(fn, file) || referenced[c.pass.TypesInfo.Defs[fn.Name]] {
				continue
			}
			if len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 {
				continue
			}
			param := fn.Type.Params.List[0]
			name := param.Names[0]
			obj := c.pass.TypesInfo.Defs[name]
			shadowedType := false
			ast.Inspect(param.Type, func(node ast.Node) bool {
				if id, ok := node.(*ast.Ident); ok && id.Name == name.Name {
					shadowedType = true
				}
				return true
			})
			// The wrapper's callee is evaluated in the test parameter's scope.
			// The copied parameter type must also still resolve in that scope.
			if name.Name == "_" || name.Name == strings.TrimSuffix(qualifier, ".Test") || shadowedType || c.assigned[obj] || hasGoDirective(fn.Doc) {
				continue
			}
			m := &migration{
				c: c, test: obj,
				values:     make(map[types.Object]bool),
				channels:   make(map[types.Object]*migrationChannel),
				timerTotal: constant.MakeInt64(0),
			}
			for _, stmt := range fn.Body.List {
				if _, ok := stmt.(*ast.GoStmt); ok {
					m.workers++
				}
			}
			if m.workers == 0 || !m.statements(fn.Body.List) || m.started != m.workers || m.joined != m.workers || len(m.sleeps)+len(m.timeouts) == 0 {
				continue
			}
			// Keep ample headroom above synctest's January 2000 epoch. Timer
			// deadline saturation could otherwise turn a strict delay bound
			// into a tie. Sum every positive duration, including branch bodies,
			// using arbitrary precision so the check itself cannot overflow.
			if len(m.timeouts) > 0 && constant.Compare(m.timerTotal, token.GTR, constant.MakeInt64(1<<62-1)) {
				continue
			}
			var typ bytes.Buffer
			if err := format.Node(&typ, c.pass.Fset, param.Type); err != nil {
				continue
			}
			edits := append([]analysis.TextEdit(nil), imports...)
			edits = append(edits,
				analysis.TextEdit{Pos: fn.Body.Lbrace + 1, End: fn.Body.Lbrace + 1, NewText: []byte("\n" + qualifier + "(" + name.Name + ", func(" + name.Name + " " + typ.String() + ") {")},
				analysis.TextEdit{Pos: fn.Body.Rbrace, End: fn.Body.Rbrace, NewText: []byte("})\n")},
			)
			fix := []analysis.SuggestedFix{{Message: "Run this test in a synctest bubble", TextEdits: edits}}
			// Identical edits are coalesced by the standard drivers. Every site
			// covered by the migration gets a fix, so no stale finding is left in
			// golangci-lint's output after a successful --fix.
			for _, sleep := range m.sleeps {
				c.migrationFixes[sleep] = fix
			}
			for _, timeout := range m.timeouts {
				c.migrationFixes[timeout] = fix
			}
		}
	}
}

func (m *migration) statements(statements []ast.Stmt) bool {
	for _, stmt := range statements {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			if !m.assign(s) {
				return false
			}
		case *ast.DeclStmt:
			decl, ok := s.Decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				return false
			}
			for _, spec := range decl.Specs {
				for _, name := range spec.(*ast.ValueSpec).Names {
					obj := m.c.pass.TypesInfo.Defs[name]
					if obj == nil || !migrationScalar(obj.Type()) {
						return false
					}
				}
			}
		case *ast.GoStmt:
			if !m.worker(s.Call) {
				return false
			}
		case *ast.ExprStmt:
			if call, ok := ast.Unparen(s.X).(*ast.CallExpr); ok {
				if m.sleep(call) {
					if m.started > 0 && m.c.positiveSleep(call) {
						m.sleeps = append(m.sleeps, call)
					}
				} else if m.joined != m.workers || !m.assertion(call) {
					return false
				}
			} else if !m.join(s.X) {
				return false
			}
		case *ast.IfStmt:
			if m.joined != m.workers || s.Init != nil || !m.scalar(s.Cond) || !m.statements(s.Body.List) {
				return false
			}
			if s.Else != nil && !m.statements([]ast.Stmt{s.Else}) {
				return false
			}
		case *ast.SelectStmt:
			if !m.timeoutSelect(s) {
				return false
			}
		case *ast.BlockStmt:
			if m.joined != m.workers || !m.statements(s.List) {
				return false
			}
		case *ast.ReturnStmt:
			if m.joined != m.workers || len(s.Results) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (m *migration) assign(s *ast.AssignStmt) bool {
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 && s.Tok == token.DEFINE {
		if call, ok := ast.Unparen(s.Rhs[0]).(*ast.CallExpr); ok && m.builtin(call.Fun, "make") && len(call.Args) >= 1 && len(call.Args) <= 2 {
			ch, ok := m.c.pass.TypesInfo.TypeOf(call).(*types.Chan)
			if !ok || ch.Dir() != types.SendRecv || !migrationElement(ch.Elem()) {
				return false
			}
			if len(call.Args) == 2 && m.c.pass.TypesInfo.Types[call.Args[1]].Value == nil {
				return false
			}
			id, ok := s.Lhs[0].(*ast.Ident)
			if !ok || id.Name == "_" {
				return false
			}
			m.channels[m.c.pass.TypesInfo.Defs[id]] = &migrationChannel{}
			return true
		}
	}
	if len(s.Rhs) == 1 && m.join(s.Rhs[0]) {
		// A channel receive may also define the boolean comma-ok result.
		if len(s.Lhs) > 2 {
			return false
		}
	} else {
		if len(s.Lhs) != len(s.Rhs) {
			return false
		}
		for _, expr := range s.Rhs {
			// Before all joins, accept only compile-time scalar values. A
			// dynamic division or shift could panic with workers still live.
			if !m.scalar(expr) || (m.joined != m.workers && m.c.pass.TypesInfo.Types[expr].Value == nil) {
				return false
			}
		}
	}
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
		return false
	}
	for _, lhs := range s.Lhs {
		id, ok := ast.Unparen(lhs).(*ast.Ident)
		if !ok {
			return false
		}
		if id.Name == "_" {
			continue
		}
		obj := m.c.pass.TypesInfo.ObjectOf(id)
		if !migrationScalar(obj.Type()) || (m.c.pass.TypesInfo.Defs[id] == nil && !m.values[obj]) {
			return false
		}
		m.values[obj] = true
	}
	return true
}

func (m *migration) worker(call *ast.CallExpr) bool {
	if m.started >= m.workers {
		return false
	}
	fn, ok := ast.Unparen(call.Fun).(*ast.FuncLit)
	if !ok || len(call.Args) != 0 || len(fn.Type.Params.List) != 0 || len(fn.Body.List) == 0 {
		return false
	}
	delay := constant.MakeInt64(0)
	for _, stmt := range fn.Body.List[:len(fn.Body.List)-1] {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			return false
		}
		call, ok := ast.Unparen(expr.X).(*ast.CallExpr)
		if !ok || !m.sleep(call) {
			return false
		}
		value := m.c.pass.TypesInfo.Types[call.Args[0]].Value
		if constant.Sign(value) > 0 {
			delay = constant.BinaryOp(delay, token.ADD, value)
		}
	}
	var channel ast.Expr
	switch last := fn.Body.List[len(fn.Body.List)-1].(type) {
	case *ast.SendStmt:
		if m.c.pass.TypesInfo.Types[last.Value].Value == nil || !migrationScalar(m.c.pass.TypesInfo.TypeOf(last.Value)) {
			return false
		}
		channel = last.Chan
	case *ast.ExprStmt:
		call, ok := ast.Unparen(last.X).(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !m.builtin(call.Fun, "close") {
			return false
		}
		channel = call.Args[0]
	default:
		return false
	}
	ch := m.channel(channel)
	if ch == nil || ch.started {
		return false
	}
	ch.started = true
	ch.delay = delay
	m.started++
	return true
}

func (m *migration) join(expr ast.Expr) bool {
	receive, ok := ast.Unparen(expr).(*ast.UnaryExpr)
	if !ok || receive.Op != token.ARROW {
		return false
	}
	ch := m.channel(receive.X)
	if ch == nil || !ch.started || ch.joined {
		return false
	}
	ch.joined = true
	m.joined++
	return true
}

func (m *migration) channel(expr ast.Expr) *migrationChannel {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		return m.channels[m.c.pass.TypesInfo.ObjectOf(id)]
	}
	return nil
}

func (m *migration) sleep(call *ast.CallExpr) bool {
	fn, ok := directObject(m.c.pass.TypesInfo, call.Fun).(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "time" || fn.Name() != "Sleep" || len(call.Args) != 1 {
		return false
	}
	value := m.c.pass.TypesInfo.Types[call.Args[0]].Value
	if value == nil {
		return false
	}
	m.addDuration(value)
	return true
}

func (m *migration) addDuration(value constant.Value) {
	if constant.Sign(value) > 0 {
		m.timerTotal = constant.BinaryOp(m.timerTotal, token.ADD, value)
	}
}

func (m *migration) timeoutSelect(stmt *ast.SelectStmt) bool {
	if len(stmt.Body.List) != 2 || m.started != m.workers || m.joined+1 != m.workers {
		return false
	}
	var result, timeout *ast.CommClause
	var after *ast.CallExpr
	var ch *migrationChannel
	for _, item := range stmt.Body.List {
		clause := item.(*ast.CommClause)
		expr := receive(clause.Comm)
		if candidate := m.channel(expr); candidate != nil {
			if result != nil || !candidate.started || candidate.joined {
				return false
			}
			result, ch = clause, candidate
			continue
		}
		call, ok := expr.(*ast.CallExpr)
		if !ok || timeout != nil || len(call.Args) != 1 {
			return false
		}
		fn, ok := directObject(m.c.pass.TypesInfo, call.Fun).(*types.Func)
		if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "time" || fn.Name() != "After" {
			return false
		}
		// Binding the timer's time.Time value could expose the fake epoch.
		if _, ok := clause.Comm.(*ast.ExprStmt); !ok {
			return false
		}
		timeout, after = clause, call
	}
	if result == nil || timeout == nil {
		return false
	}
	duration := m.c.pass.TypesInfo.Types[after.Args[0]].Value
	if duration == nil || constant.Sign(duration) <= 0 || !constant.Compare(ch.delay, token.LSS, duration) {
		return false
	}
	// All worker operations before the final signal are constant Sleeps.
	// Fake time cannot advance while that worker is runnable, so its signal
	// becomes ready strictly before the timer. The timeout branch cannot run
	// in the bubble; the receive branch joins the final outstanding worker.
	// Still validate both bodies to preserve the whole-test safety boundary.
	if !m.statements([]ast.Stmt{result.Comm}) || !m.statements(result.Body) || !m.statements(timeout.Body) {
		return false
	}
	m.addDuration(duration)
	m.timeouts = append(m.timeouts, after)
	return true
}

func (m *migration) builtin(expr ast.Expr, name string) bool {
	fn, ok := directObject(m.c.pass.TypesInfo, expr).(*types.Builtin)
	return ok && fn.Name() == name
}

func (m *migration) assertion(call *ast.CallExpr) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := ast.Unparen(sel.X).(*ast.Ident)
	if !ok || m.c.pass.TypesInfo.ObjectOf(id) != m.test {
		return false
	}
	switch sel.Sel.Name {
	case "Error", "Errorf", "Fatal", "Fatalf", "Log", "Logf", "Fail", "FailNow":
	default:
		return false
	}
	for _, arg := range call.Args {
		if !m.scalar(arg) {
			return false
		}
	}
	return true
}

func (m *migration) scalar(expr ast.Expr) bool {
	if !migrationScalar(m.c.pass.TypesInfo.TypeOf(expr)) {
		return false
	}
	if m.c.pass.TypesInfo.Types[expr].Value != nil {
		return true
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return m.values[m.c.pass.TypesInfo.ObjectOf(e)]
	case *ast.BinaryExpr:
		return m.scalar(e.X) && m.scalar(e.Y)
	case *ast.UnaryExpr:
		return (e.Op == token.ADD || e.Op == token.SUB || e.Op == token.NOT || e.Op == token.XOR) && m.scalar(e.X)
	}
	return false
}

func migrationScalar(typ types.Type) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if basic, ok := typ.(*types.Basic); ok {
		return basic.Info()&(types.IsBoolean|types.IsNumeric|types.IsString) != 0
	}
	named, ok := typ.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "time" && named.Obj().Name() == "Duration"
}

func migrationElement(typ types.Type) bool {
	if migrationScalar(typ) {
		return true
	}
	strct, ok := types.Unalias(typ).(*types.Struct)
	return ok && strct.NumFields() == 0
}

func directObject(info *types.Info, expr ast.Expr) types.Object {
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return info.ObjectOf(e)
	case *ast.SelectorExpr:
		return info.ObjectOf(e.Sel)
	}
	return nil
}

func hasLineDirective(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//line ") || strings.HasPrefix(comment.Text, "/*line ") {
				return true
			}
		}
	}
	return false
}

func hasGoDirective(group *ast.CommentGroup) bool {
	if group != nil {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:") {
				return true
			}
		}
	}
	return false
}

// All fixes in a file share this exact import edit. Keeping it in every fix
// also lets a driver apply just one test's migration after filtering issues.
func (c *checker) migrationImport(file *ast.File) (string, []analysis.TextEdit) {
	var blank *ast.ImportSpec
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "testing/synctest" {
			continue
		}
		if spec.Name == nil {
			return "synctest.Test", nil
		}
		switch spec.Name.Name {
		case ".":
			return "Test", nil
		case "_":
			blank = spec
		default:
			return spec.Name.Name + ".Test", nil
		}
	}
	used := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			used[id.Name] = true
		}
		return true
	})
	name := "synctest"
	for i := 1; used[name] || c.pass.Pkg.Scope().Lookup(name) != nil; i++ {
		name = "synctest" + strconv.Itoa(i)
	}
	if blank != nil {
		return name + ".Test", []analysis.TextEdit{{Pos: blank.Name.Pos(), End: blank.Name.End(), NewText: []byte(name)}}
	}
	prefix := ""
	if name != "synctest" {
		prefix = name + " "
	}
	for _, decl := range file.Decls {
		if imp, ok := decl.(*ast.GenDecl); ok && imp.Tok == token.IMPORT && imp.Lparen.IsValid() && len(imp.Specs) > 0 {
			cgo := false
			for _, spec := range imp.Specs {
				path, _ := strconv.Unquote(spec.(*ast.ImportSpec).Path.Value)
				cgo = cgo || path == "C"
			}
			if !cgo {
				first := imp.Specs[0].(*ast.ImportSpec)
				pos := first.Pos()
				if first.Doc != nil {
					pos = first.Doc.Pos()
				}
				return name + ".Test", []analysis.TextEdit{{Pos: pos, End: pos, NewText: []byte(prefix + "\"testing/synctest\"\n")}}
			}
		}
	}
	for _, decl := range file.Decls {
		if imp, ok := decl.(*ast.GenDecl); ok && imp.Tok == token.IMPORT {
			pos := imp.Pos()
			if imp.Doc != nil {
				pos = imp.Doc.Pos()
			}
			return name + ".Test", []analysis.TextEdit{{Pos: pos, End: pos, NewText: []byte("import " + prefix + "\"testing/synctest\"\n\n")}}
		}
	}
	return "", nil
}
