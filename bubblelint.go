// Package bubblelint checks testing/synctest usage and suggests tests to migrate.
package bubblelint

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"go/version"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

// Options controls independent groups of checks.
type Options struct {
	CheckMisuse bool
	Suggest     bool
}

// New returns an independent analyzer. Nil enables misuse checks only.
func New(options *Options) *analysis.Analyzer {
	settings := Options{CheckMisuse: true}
	if options != nil {
		settings = *options
	}
	a := &analysis.Analyzer{
		Name: "bubblelint",
		Doc:  "check testing/synctest usage and find timeout-based concurrent tests",
	}
	a.Flags.BoolVar(&settings.CheckMisuse, "check-misuse", settings.CheckMisuse, "check definite synctest misuse on known test call paths")
	a.Flags.BoolVar(&settings.Suggest, "suggest", settings.Suggest, "suggest concurrent tests to consider migrating to synctest")
	a.Run = func(pass *analysis.Pass) (any, error) {
		if settings.CheckMisuse || settings.Suggest {
			newChecker(pass, settings).run()
		}
		return nil, nil
	}
	return a
}

type function struct {
	body   *ast.BlockStmt
	params *ast.FieldList
}

type state struct {
	bubble     bool
	background bool
	async      bool
	bubbledT   map[types.Object]bool
}

type visitKey struct {
	body       *ast.BlockStmt
	bubble     bool
	background bool
	async      bool
	bindings   string
}

type checker struct {
	pass      *analysis.Pass
	options   Options
	functions map[*types.Func]function
	aliases   map[types.Object]ast.Expr
	assigned  map[types.Object]bool
	timeouts  map[*ast.CallExpr]bool
	visited   map[visitKey]bool
	reported  map[token.Pos]map[string]bool
	roots     []function
}

func newChecker(pass *analysis.Pass, options Options) *checker {
	return &checker{
		pass: pass, options: options,
		functions: make(map[*types.Func]function),
		aliases:   make(map[types.Object]ast.Expr),
		assigned:  make(map[types.Object]bool),
		timeouts:  make(map[*ast.CallExpr]bool),
		visited:   make(map[visitKey]bool),
		reported:  make(map[token.Pos]map[string]bool),
	}
}

func (c *checker) run() {
	// A function value or T alias is followed only if it has one assignment.
	writes := make(map[types.Object]int)
	for _, file := range c.pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncDecl:
				if n.Body != nil && n.Recv == nil {
					obj, _ := c.pass.TypesInfo.Defs[n.Name].(*types.Func)
					fn := function{n.Body, n.Type.Params}
					c.functions[obj] = fn
					if c.isTest(n, file) {
						c.roots = append(c.roots, fn)
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						obj := c.pass.TypesInfo.ObjectOf(id)
						writes[obj]++
						if len(n.Lhs) == len(n.Rhs) {
							c.aliases[obj] = n.Rhs[i]
						}
					}
				}
			case *ast.ValueSpec:
				for i, id := range n.Names {
					obj := c.pass.TypesInfo.Defs[id]
					writes[obj]++
					if len(n.Names) == len(n.Values) {
						c.aliases[obj] = n.Values[i]
					}
				}
			case *ast.IncDecStmt:
				if id, ok := n.X.(*ast.Ident); ok {
					writes[c.pass.TypesInfo.ObjectOf(id)]++
				}
			case *ast.UnaryExpr:
				if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND {
					writes[c.pass.TypesInfo.ObjectOf(id)] += 2
				}
			}
			return true
		})
	}
	for obj := range c.aliases {
		if obj == nil || writes[obj] != 1 {
			delete(c.aliases, obj)
		}
	}
	for obj, count := range writes {
		c.assigned[obj] = count > 0
	}
	for _, file := range c.pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if n, ok := node.(*ast.SelectStmt); ok {
				c.findTimeouts(n)
			}
			return true
		})
	}
	for _, root := range c.roots {
		c.invoke(root, state{bubbledT: make(map[types.Object]bool)}, nil, false)
	}
}

func (c *checker) isTest(fn *ast.FuncDecl, file *ast.File) bool {
	if !strings.HasSuffix(c.pass.Fset.Position(file.Pos()).Filename, "_test.go") ||
		!strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	suffix := strings.TrimPrefix(fn.Name.Name, "Test")
	if suffix != "" {
		for _, r := range suffix {
			if unicode.IsLower(r) {
				return false
			}
			break
		}
	}
	obj, _ := c.pass.TypesInfo.Defs[fn.Name].(*types.Func)
	if obj == nil {
		return false
	}
	sig := obj.Type().(*types.Signature)
	return !sig.Variadic() && sig.Params().Len() == 1 && sig.Results().Len() == 0 && isTestingT(sig.Params().At(0).Type())
}

func isTestingT(t types.Type) bool {
	pointer, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "testing" && named.Obj().Name() == "T"
}

func (c *checker) resolve(expr ast.Expr) ast.Expr {
	seen := make(map[types.Object]bool)
	for {
		expr = ast.Unparen(expr)
		id, ok := expr.(*ast.Ident)
		if !ok {
			return expr
		}
		obj := c.pass.TypesInfo.ObjectOf(id)
		next := c.aliases[obj]
		if next == nil || seen[obj] {
			return expr
		}
		seen[obj] = true
		expr = next
	}
}

func (c *checker) object(expr ast.Expr) *types.Func {
	expr = c.resolve(expr)
	switch x := expr.(type) {
	case *ast.Ident:
		fn, _ := c.pass.TypesInfo.ObjectOf(x).(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := c.pass.TypesInfo.ObjectOf(x.Sel).(*types.Func)
		return fn
	case *ast.IndexExpr:
		return c.object(x.X)
	case *ast.IndexListExpr:
		return c.object(x.X)
	}
	return nil
}

func (c *checker) named(expr ast.Expr, path, name string) bool {
	fn := c.object(expr)
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == path && fn.Name() == name
}

func (c *checker) callable(expr ast.Expr) (function, bool) {
	if literal, ok := c.resolve(expr).(*ast.FuncLit); ok {
		return function{literal.Body, literal.Type.Params}, true
	}
	obj := c.object(expr)
	if obj != nil {
		fn, ok := c.functions[obj.Origin()]
		return fn, ok
	}
	return function{}, false
}

func (c *checker) bubbled(expr ast.Expr, s state) bool {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok && s.bubbledT[c.pass.TypesInfo.ObjectOf(id)] {
		return true
	}
	if id, ok := c.resolve(expr).(*ast.Ident); ok {
		return s.bubbledT[c.pass.TypesInfo.ObjectOf(id)]
	}
	return false
}

func (c *checker) invoke(fn function, s state, args []ast.Expr, bubbleCallback bool) bool {
	bindings := make(map[types.Object]bool, len(s.bubbledT))
	for obj, value := range s.bubbledT {
		bindings[obj] = value
	}
	index := 0
	if fn.params != nil {
		for _, field := range fn.params.List {
			for _, name := range field.Names {
				value := bubbleCallback && index == 0
				if !bubbleCallback && index < len(args) {
					value = c.bubbled(args[index], s)
				}
				obj := c.pass.TypesInfo.Defs[name]
				bindings[obj] = value && !c.assigned[obj]
				index++
			}
		}
	}
	s.bubbledT = bindings
	var positions []int
	for obj, value := range bindings {
		if value && obj != nil {
			positions = append(positions, int(obj.Pos()))
		}
	}
	sort.Ints(positions)
	var keyText strings.Builder
	for _, pos := range positions {
		keyText.WriteString(strconv.Itoa(pos))
		keyText.WriteByte(',')
	}
	key := visitKey{fn.body, s.bubble, s.background, s.async, keyText.String()}
	if result, seen := c.visited[key]; seen {
		return result
	}
	// Seed the result before descending so recursive helpers terminate.
	c.visited[key] = s.async
	c.walk(fn.body, &s)
	c.visited[key] = s.async
	return s.async
}

func (c *checker) walk(node ast.Node, s *state) {
	ast.Inspect(node, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			// A closure's location does not tell us where it executes.
			return false
		case *ast.GoStmt:
			c.walk(n.Call.Fun, s)
			for _, arg := range n.Call.Args {
				c.walk(arg, s)
			}
			s.async = true
			worker := *s
			worker.background = true
			c.call(n.Call, &worker)
			return false
		case *ast.CallExpr:
			c.walk(n.Fun, s)
			for _, arg := range n.Args {
				c.walk(arg, s)
			}
			c.call(n, s)
			return false
		}
		return true
	})
}

func (c *checker) call(call *ast.CallExpr, s *state) {
	if c.named(call.Fun, "testing/synctest", "Test") || c.named(call.Fun, "testing/synctest", "Run") {
		if s.bubble && c.options.CheckMisuse {
			c.report(call, "ST002", "cannot start a synctest bubble from inside another bubble")
		}
		index := 1
		if c.named(call.Fun, "testing/synctest", "Run") {
			index = 0
		}
		if index < len(call.Args) {
			if callback, ok := c.callable(call.Args[index]); ok {
				inner := *s
				inner.bubble, inner.background, inner.async = true, false, false
				c.invoke(callback, inner, nil, index == 1)
			}
		}
		return
	}
	if c.named(call.Fun, "testing/synctest", "Wait") && !s.bubble && c.options.CheckMisuse {
		c.report(call, "ST001", "synctest.Wait is called outside a synctest bubble on this test call path")
	}
	if c.options.Suggest && !s.bubble && !s.background && c.supportsSynctest() {
		if s.async && c.named(call.Fun, "time", "Sleep") && c.positiveSleep(call) {
			c.report(call, "ST100", "consider synctest.Test and explicit synchronization for this sleep after starting asynchronous work; verify bubble compatibility")
		}
		if c.timeouts[call] {
			c.report(call, "ST101", "consider synctest.Test for this timeout-based channel assertion; verify that the channel and its producers can run in the same bubble")
		}
	}
	if c.named(call.Fun, "context", "AfterFunc") || c.named(call.Fun, "time", "AfterFunc") {
		s.async = true
		// Context cancellation may happen in a different bubble. Its callback
		// cannot be assigned an execution context from registration alone.
		if c.named(call.Fun, "time", "AfterFunc") && len(call.Args) == 2 {
			if fn, ok := c.callable(call.Args[1]); ok {
				worker := *s
				worker.background = true
				c.invoke(fn, worker, nil, false)
			}
		}
	}
	if selector, ok := c.resolve(call.Fun).(*ast.SelectorExpr); ok {
		fn := c.object(call.Fun)
		if fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "sync" && fn.Name() == "Go" && len(call.Args) == 1 {
			s.async = true
			if callback, ok := c.callable(call.Args[0]); ok {
				worker := *s
				worker.background = true
				c.invoke(callback, worker, nil, false)
			}
		}
		if fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "testing" && isTestingT(c.pass.TypesInfo.TypeOf(selector.X)) {
			bubbled := c.bubbled(selector.X, *s)
			if bubbled && c.options.CheckMisuse {
				switch fn.Name() {
				case "Run", "Parallel", "Deadline":
					c.report(call, "ST003", "testing.T."+fn.Name()+" must not be called on the *testing.T provided by synctest.Test")
				}
			}
			index := -1
			if fn.Name() == "Run" {
				index = 1
			} else if fn.Name() == "Cleanup" {
				index = 0
			}
			if index >= 0 && index < len(call.Args) {
				if callback, ok := c.callable(call.Args[index]); ok {
					inner := *s
					inner.async = false
					if fn.Name() == "Cleanup" {
						// Cleanup runs with its owning T, possibly after the
						// registering goroutine's bubble has exited.
						inner.bubble = bubbled
					}
					// Run starts a goroutine in the caller's bubble. Its T
					// is a normal subtest T, not synctest.Test's special T.
					c.invoke(callback, inner, nil, false)
				}
			}
		}
	}
	if fn, ok := c.callable(call.Fun); ok {
		s.async = c.invoke(fn, *s, call.Args, false) || s.async
	}
}

func (c *checker) supportsSynctest() bool {
	v := c.pass.Pkg.GoVersion()
	return v == "" || version.Compare(v, "go1.25") >= 0
}

func (c *checker) positiveSleep(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	value := c.pass.TypesInfo.Types[call.Args[0]].Value
	return value == nil || constant.Sign(value) > 0
}

func receive(comm ast.Stmt) ast.Expr {
	var expr ast.Expr
	switch stmt := comm.(type) {
	case *ast.ExprStmt:
		expr = stmt.X
	case *ast.AssignStmt:
		if len(stmt.Rhs) == 1 {
			expr = stmt.Rhs[0]
		}
	}
	if expr != nil {
		if unary, ok := ast.Unparen(expr).(*ast.UnaryExpr); ok && unary.Op == token.ARROW {
			return ast.Unparen(unary.X)
		}
	}
	return nil
}

func (c *checker) findTimeouts(stmt *ast.SelectStmt) {
	var timeouts []*ast.CallExpr
	peer := false
	for _, item := range stmt.Body.List {
		expr := receive(item.(*ast.CommClause).Comm)
		if expr == nil {
			continue
		}
		if call, ok := expr.(*ast.CallExpr); ok && c.named(call.Fun, "time", "After") {
			timeouts = append(timeouts, call)
		} else if !c.pass.TypesInfo.Types[expr].IsNil() {
			peer = true
		}
	}
	if peer {
		for _, call := range timeouts {
			c.timeouts[call] = true
		}
	}
}

func (c *checker) report(node ast.Node, code, message string) {
	if c.reported[node.Pos()] == nil {
		c.reported[node.Pos()] = make(map[string]bool)
	}
	if c.reported[node.Pos()][code] {
		return
	}
	c.reported[node.Pos()][code] = true
	c.pass.Report(analysis.Diagnostic{
		Pos: node.Pos(), End: node.End(), Category: code, Message: code + ": " + message,
	})
}
