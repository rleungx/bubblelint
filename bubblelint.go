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

// testContext separates the T's cleanup execution context from the restrictions
// on the special T supplied by synctest.Test. A missing binding is unknown.
type testContext struct {
	bubble   bool
	synctest bool
}

type state struct {
	bubble     bool
	background bool
	async      bool
	tests      map[types.Object]testContext
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

	parallelFixes  map[*ast.CallExpr][]analysis.SuggestedFix
	migrationFixes map[*ast.CallExpr][]analysis.SuggestedFix
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
	for _, file := range c.pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		for _, decl := range file.Decls {
			if n, ok := decl.(*ast.FuncDecl); ok && n.Body != nil && n.Recv == nil {
				obj, _ := c.pass.TypesInfo.Defs[n.Name].(*types.Func)
				fn := function{n.Body, n.Type.Params}
				c.functions[obj] = fn
				if c.isTest(n, file) {
					c.roots = append(c.roots, fn)
				}
			}
		}
	}
	// All checks start from tests. Production-only packages need no body walks.
	if len(c.roots) == 0 {
		return
	}
	// A function value or T alias is followed only if it has one assignment.
	writes := make(map[types.Object]int)
	entryBindings := make(map[types.Object]bool)
	recordEntry := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				if obj := c.pass.TypesInfo.Defs[name]; obj != nil {
					writes[obj]++
					entryBindings[obj] = true
				}
			}
		}
	}
	for _, file := range c.pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncDecl:
				recordEntry(n.Recv)
			case *ast.FuncType:
				// Parameters and named results already have a value on entry.
				recordEntry(n.Params)
				recordEntry(n.Results)
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if id, ok := ast.Unparen(lhs).(*ast.Ident); ok {
						obj := c.pass.TypesInfo.ObjectOf(id)
						writes[obj]++
						if len(n.Lhs) == len(n.Rhs) {
							c.aliases[obj] = n.Rhs[i]
						}
					}
				}
			case *ast.RangeStmt:
				for _, lhs := range []ast.Expr{n.Key, n.Value} {
					if id, ok := ast.Unparen(lhs).(*ast.Ident); ok {
						writes[c.pass.TypesInfo.ObjectOf(id)]++
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
				if id, ok := ast.Unparen(n.X).(*ast.Ident); ok {
					writes[c.pass.TypesInfo.ObjectOf(id)]++
				}
			case *ast.UnaryExpr:
				if id, ok := ast.Unparen(n.X).(*ast.Ident); ok && n.Op == token.AND {
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
		if entryBindings[obj] {
			c.assigned[obj] = count > 1
		}
	}
	if c.options.CheckMisuse {
		c.findParallelFixes()
	}
	if c.options.Suggest {
		c.findMigrationFixes()
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
	}
	for _, root := range c.roots {
		c.invoke(root, state{}, nil, &testContext{})
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

// callback follows a known callback body or checks a standard-library function
// value whose body is outside this package. Diagnostics belong to the callback
// expression at this registration site, including when it is an alias.
func (c *checker) callback(expr ast.Expr, s state, callbackT *testContext) {
	if fn, ok := c.callable(expr); ok {
		c.invoke(fn, s, nil, callbackT)
		return
	}
	if !c.options.CheckMisuse {
		return
	}
	c.checkBubbleOnly(expr, expr, s)
	selector, ok := c.resolve(expr).(*ast.SelectorExpr)
	if !ok {
		return
	}
	selection := c.pass.TypesInfo.Selections[selector]
	if selection == nil || !isTestingT(selection.Recv()) || !c.named(expr, "testing", "Parallel") {
		return
	}
	context, known := c.testContext(selector.X, s)
	if selection.Kind() == types.MethodExpr {
		known = callbackT != nil
		if known {
			context = *callbackT
		}
	}
	if known && context.synctest {
		c.report(expr, "ST003", "testing.T.Parallel must not be called on the *testing.T provided by synctest.Test")
	}
}

func (c *checker) checkBubbleOnly(expr ast.Expr, site ast.Node, s state) {
	if !c.options.CheckMisuse || s.bubble {
		return
	}
	fn := c.object(expr)
	if fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "testing/synctest" && (fn.Name() == "Wait" || fn.Name() == "Sleep") {
		c.report(site, "ST001", "synctest."+fn.Name()+" is called outside a synctest bubble on this test call path")
	}
}

// methodCall gives method values and method expressions the same receiver and
// argument layout, including calls through immutable aliases.
func (c *checker) methodCall(call *ast.CallExpr) (*types.Func, ast.Expr, []ast.Expr) {
	selector, ok := c.resolve(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, nil, nil
	}
	selection := c.pass.TypesInfo.Selections[selector]
	if selection == nil {
		return nil, nil, nil
	}
	receiver, args := selector.X, call.Args
	if selection.Kind() == types.MethodExpr {
		if len(args) == 0 {
			return nil, nil, nil
		}
		receiver, args = args[0], args[1:]
	}
	fn, _ := selection.Obj().(*types.Func)
	return fn, receiver, args
}

func (c *checker) testContext(expr ast.Expr, s state) (testContext, bool) {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		if context, known := s.tests[c.pass.TypesInfo.ObjectOf(id)]; known {
			return context, true
		}
	}
	if id, ok := c.resolve(expr).(*ast.Ident); ok {
		context, known := s.tests[c.pass.TypesInfo.ObjectOf(id)]
		return context, known
	}
	return testContext{}, false
}

func (c *checker) invoke(fn function, s state, args []ast.Expr, callbackT *testContext) bool {
	bindings := make(map[types.Object]testContext)
	for obj, context := range s.tests {
		// Only lexical captures are available to the callee. Retaining the
		// dynamic call stack here creates a different cache key for every path
		// through a shared helper graph.
		if scope := obj.Parent(); scope != nil && scope.Contains(fn.body.Pos()) {
			bindings[obj] = context
		}
	}
	index := 0
	if fn.params != nil {
		for _, field := range fn.params.List {
			for _, name := range field.Names {
				var context testContext
				known := false
				if callbackT != nil && index == 0 {
					context, known = *callbackT, true
				} else if index < len(args) {
					context, known = c.testContext(args[index], s)
				}
				obj := c.pass.TypesInfo.Defs[name]
				delete(bindings, obj)
				if known && obj != nil && !c.assigned[obj] {
					bindings[obj] = context
				}
				index++
			}
		}
	}
	s.tests = bindings
	type binding struct {
		pos     token.Pos
		context testContext
	}
	var ordered []binding
	for obj, context := range bindings {
		ordered = append(ordered, binding{obj.Pos(), context})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].pos < ordered[j].pos })
	var keyText strings.Builder
	for _, binding := range ordered {
		keyText.WriteString(strconv.Itoa(int(binding.pos)))
		if binding.context.bubble {
			keyText.WriteByte('b')
		}
		if binding.context.synctest {
			keyText.WriteByte('s')
		}
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
			inner := *s
			inner.bubble, inner.background, inner.async = true, false, false
			var callbackT *testContext
			if index == 1 {
				callbackT = &testContext{bubble: true, synctest: true}
			}
			c.callback(call.Args[index], inner, callbackT)
		}
		return
	}
	c.checkBubbleOnly(call.Fun, call, *s)
	if c.options.Suggest && !s.bubble && !s.background && c.supportsSynctest() {
		if s.async && c.named(call.Fun, "time", "Sleep") && c.positiveSleep(call) {
			c.report(call, "ST100", "consider synctest.Test and explicit synchronization for this sleep after starting asynchronous work; verify bubble compatibility", c.migrationFixes[call]...)
		}
		if c.timeouts[call] {
			c.report(call, "ST101", "consider synctest.Test for this timeout-based channel assertion; verify that the channel and its producers can run in the same bubble", c.migrationFixes[call]...)
		}
	}
	if c.named(call.Fun, "context", "AfterFunc") || c.named(call.Fun, "time", "AfterFunc") {
		s.async = true
		// Context cancellation may happen in a different bubble. Its callback
		// cannot be assigned an execution context from registration alone.
		if c.named(call.Fun, "time", "AfterFunc") && len(call.Args) == 2 {
			worker := *s
			worker.background = true
			c.callback(call.Args[1], worker, nil)
		}
	}
	if fn, receiver, args := c.methodCall(call); fn != nil && fn.Pkg() != nil {
		if fn.Pkg().Path() == "sync" && fn.Name() == "Go" && len(args) == 1 {
			s.async = true
			worker := *s
			worker.background = true
			c.callback(args[0], worker, nil)
		}
		if fn.Pkg().Path() == "testing" && isTestingT(c.pass.TypesInfo.TypeOf(receiver)) {
			context, known := c.testContext(receiver, *s)
			if known && context.synctest && c.options.CheckMisuse {
				switch fn.Name() {
				case "Run", "Parallel", "Deadline":
					c.report(call, "ST003", "testing.T."+fn.Name()+" must not be called on the *testing.T provided by synctest.Test", c.parallelFixes[call]...)
				}
			}
			index := -1
			if fn.Name() == "Run" {
				index = 1
			} else if fn.Name() == "Cleanup" {
				index = 0
			}
			if index >= 0 && index < len(args) {
				inner := *s
				inner.async = false
				var callbackT *testContext
				if fn.Name() == "Cleanup" {
					if !known {
						return
					}
					// Cleanup runs with its owning T, possibly after the
					// registering goroutine's bubble has exited.
					inner.bubble = context.bubble
				} else {
					// Run creates an ordinary T in the caller's bubble.
					callbackT = &testContext{bubble: s.bubble}
				}
				c.callback(args[index], inner, callbackT)
			}
		}
	}
	if fn, ok := c.callable(call.Fun); ok {
		s.async = c.invoke(fn, *s, call.Args, nil) || s.async
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

func (c *checker) report(node ast.Node, code, message string, fixes ...analysis.SuggestedFix) {
	if c.reported[node.Pos()] == nil {
		c.reported[node.Pos()] = make(map[string]bool)
	}
	if c.reported[node.Pos()][code] {
		return
	}
	c.reported[node.Pos()][code] = true
	c.pass.Report(analysis.Diagnostic{
		Pos: node.Pos(), End: node.End(), Category: code, Message: code + ": " + message,
		SuggestedFixes: fixes,
	})
}
