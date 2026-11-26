package bubblelint

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

type migrationEffect uint8

const (
	effectNetwork migrationEffect = iota
	effectProcess
	effectOutsideCallback
	effectTesting
	effectMutex
	effectInterface
	effectFormatting
	effectOpaqueCall
	effectClock
	effectShared
	effectLoop
	effectRecursion
	effectPolling
	effectLifecycle
)

func (e migrationEffect) blocked() bool {
	return e == effectNetwork || e == effectProcess || e == effectOutsideCallback || e == effectTesting
}

func (e migrationEffect) reviewOnly() bool {
	return e == effectClock || e == effectShared || e == effectLoop || e == effectRecursion || e == effectLifecycle
}

// Positions in facts use filenames and line numbers: token.Pos values belong to
// the exporting analysis pass and cannot be used by an importing package.
type migrationFrame struct {
	Symbol   string
	Position token.Position
}

type migrationReason struct {
	Kind        migrationEffect
	Message     string
	Trace       []migrationFrame
	Conditional bool
}

type migrationReasonSet map[migrationEffect]migrationReason

// Prefer required paths, then the shortest witness for each effect. Recursive
// and shared call graphs keep bounded summaries; omitted paths are not safe.
func (s migrationReasonSet) add(reason migrationReason) bool {
	previous, exists := s[reason.Kind]
	if exists && previous.Conditional != reason.Conditional {
		if !previous.Conditional {
			return false
		}
	} else if exists && (len(previous.Trace) < len(reason.Trace) ||
		len(previous.Trace) == len(reason.Trace) && migrationReasonKey(previous) <= migrationReasonKey(reason)) {
		return false
	}
	s[reason.Kind] = reason
	return true
}

func migrationReasonKey(reason migrationReason) string {
	var key strings.Builder
	key.WriteString(reason.Message)
	for _, frame := range reason.Trace {
		fmt.Fprintf(&key, "|%s:%s:%d:%d", frame.Symbol, frame.Position.Filename, frame.Position.Line, frame.Position.Column)
	}
	return key.String()
}

func (s migrationReasonSet) ordered() []migrationReason {
	reasons := make([]migrationReason, 0, len(s))
	for _, reason := range s {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i].Kind < reasons[j].Kind })
	return reasons
}

type migrationFact struct {
	Reasons  []migrationReason
	Timer    bool
	Decision []byte
	program  *migrationProgram
	decoded  bool
}

func (*migrationFact) AFact() {}

type migrationEffectsResult struct {
	reasons        map[*ast.BlockStmt]migrationReasonSet
	barriers       map[*ast.BlockStmt]bool
	reviewBarriers map[*ast.BlockStmt]bool
	timers         map[*ast.BlockStmt]bool
	imported       map[*types.Func]*migrationFact
	entries        map[*ast.BlockStmt][]migrationEntry
}

func newMigrationEffectsAnalyzer(enabled func() bool) *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:       "bubblelint_effects",
		Doc:        "summarize effects relevant to synctest migration across packages",
		FactTypes:  []analysis.Fact{new(migrationFact)},
		ResultType: reflect.TypeFor[*migrationEffectsResult](),
		Run: func(pass *analysis.Pass) (any, error) {
			if !enabled() {
				return (*migrationEffectsResult)(nil), nil
			}
			c := newChecker(pass, allChecks)
			c.collect(true)
			if len(c.roots) == 0 && migrationStandardLibrarySource(pass) {
				// Standard library dependencies use public operation models.
				// Implementation summaries can expose runtime details that do
				// not describe the API's bubble behavior.
				return (*migrationEffectsResult)(nil), nil
			}
			c.effects = &migrationEffectsResult{
				reasons:  make(map[*ast.BlockStmt]migrationReasonSet),
				timers:   make(map[*ast.BlockStmt]bool),
				imported: make(map[*types.Func]*migrationFact),
			}
			c.findMigrationBarriers()
			c.coarseEffects = c.effects
			c.prepareMigrationPrograms()
			var ordered []*types.Func
			for fn := range c.functions {
				if fn != nil {
					ordered = append(ordered, fn)
				}
			}
			sort.Slice(ordered, func(i, j int) bool { return ordered[i].Pos() < ordered[j].Pos() })
			for _, fn := range ordered {
				budget := 128
				c.migrationProgram(fn, c.functions[fn], &budget)
			}
			// A cycle encountered while building one entry initially retains a
			// coarse fallback. Revisit each recursive entry with the other
			// completed programs available, within the same work limit.
			for _, fn := range ordered {
				body := c.functions[fn]
				if _, recursive := c.coarseEffects.reasons[body.body][effectRecursion]; recursive {
					delete(c.programs, body.body)
					budget := 128
					c.migrationProgram(fn, body, &budget)
				}
			}
			c.effects = &migrationEffectsResult{
				reasons: make(map[*ast.BlockStmt]migrationReasonSet), timers: make(map[*ast.BlockStmt]bool), imported: c.coarseEffects.imported,
			}
			c.findMigrationBarriers()
			c.cacheReferencedMigrationFacts()
			for fn, body := range c.functions {
				if fn != nil && fn.Exported() {
					fact := &migrationFact{
						Reasons: c.effects.reasons[body.body].ordered(),
						Timer:   c.effects.timers[body.body],
					}
					// Propagate decisions as data. Decode their recursive structure
					// only when a caller uses the function, rather than at each
					// intermediate package in the analysis graph.
					if program := c.programs[body.body]; program != nil {
						var err error
						fact.Decision, err = json.Marshal(program)
						if err != nil {
							return nil, fmt.Errorf("encode migration decision for %s: %w", fn.FullName(), err)
						}
					}
					pass.ExportObjectFact(fn, fact)
				}
			}
			return c.effects, nil
		},
	}
}

func migrationStandardLibrarySource(pass *analysis.Pass) bool {
	if pass.Module != nil && pass.Module.Path != "" || len(pass.Files) == 0 || build.Default.GOROOT == "" {
		return false
	}
	root := filepath.Join(build.Default.GOROOT, "src")
	for _, file := range pass.Files {
		name := pass.Fset.PositionFor(file.Pos(), false).Filename
		relative, err := filepath.Rel(root, name)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return false
		}
	}
	return true
}

func (c *checker) addMigrationReason(body *ast.BlockStmt, kind migrationEffect, node ast.Node, symbol, message string) {
	if c.effects.reasons[body] == nil {
		c.effects.reasons[body] = make(migrationReasonSet)
	}
	c.effects.reasons[body].add(migrationReason{
		Kind: kind, Message: message,
		Trace:       []migrationFrame{{Symbol: symbol, Position: c.pass.Fset.PositionFor(node.Pos(), false)}},
		Conditional: c.effectConditional,
	})
}

func (c *checker) importMigrationFact(fn *types.Func) *migrationFact {
	fact := c.cacheMigrationFact(fn)
	if fact != nil && !fact.decoded {
		fact.decoded = true
		if len(fact.Decision) != 0 {
			var program migrationProgram
			if json.Unmarshal(fact.Decision, &program) == nil {
				fact.program = &program
			}
		}
	}
	return fact
}

func (c *checker) cacheMigrationFact(fn *types.Func) *migrationFact {
	fn = fn.Origin()
	if fact, known := c.effects.imported[fn]; known {
		return fact
	}
	var fact migrationFact
	if c.pass.ImportObjectFact != nil && fn.Pkg() != nil && fn.Pkg() != c.pass.Pkg && c.pass.ImportObjectFact(fn, &fact) {
		c.effects.imported[fn] = &fact
		return &fact
	}
	c.effects.imported[fn] = nil
	return nil
}

// The dependent analyzer resolves test-specific dispatch after this pass ends.
// Import facts for referenced functions and concrete method sets here: facts
// belong to this analyzer and cannot be imported through the dependent pass.
// Keep decision payloads encoded until a resolved call actually needs them.
func (c *checker) cacheReferencedMigrationFacts() {
	seen := make(map[*types.Named]bool)
	for _, info := range c.pass.TypesInfo.Types {
		t := types.Unalias(info.Type)
		if pointer, ok := t.(*types.Pointer); ok {
			t = types.Unalias(pointer.Elem())
		}
		named, ok := t.(*types.Named)
		if !ok || seen[named] {
			continue
		}
		seen[named] = true
		methods := types.NewMethodSet(types.NewPointer(named))
		for i := 0; i < methods.Len(); i++ {
			fn := methods.At(i).Obj().(*types.Func)
			if fn.Pkg() != c.pass.Pkg && fn.Exported() {
				c.cacheMigrationFact(fn)
			}
		}
	}
	for _, obj := range c.pass.TypesInfo.Uses {
		if fn, ok := obj.(*types.Func); ok && fn.Pkg() != c.pass.Pkg {
			c.cacheMigrationFact(fn)
		}
	}
}

func (c *checker) mergeMigrationFact(body *ast.BlockStmt, fn *types.Func, call *ast.CallExpr, fact *migrationFact) {
	if c.effects.reasons[body] == nil {
		c.effects.reasons[body] = make(migrationReasonSet)
	}
	frame := migrationFrame{Symbol: fn.FullName(), Position: c.pass.Fset.PositionFor(call.Pos(), false)}
	for _, reason := range fact.Reasons {
		reason.Trace = append([]migrationFrame{frame}, reason.Trace...)
		reason.Conditional = reason.Conditional || c.effectConditional
		c.effects.reasons[body].add(reason)
	}
}

func (c *checker) migrationInvocationEffects(body *ast.BlockStmt, call *ast.CallExpr, fn *types.Func) *migrationFact {
	if migrationAtomicOperation(fn) != "" {
		return nil
	}
	add := func(kind migrationEffect, message string) {
		c.addMigrationReason(body, kind, call, fn.FullName(), message)
	}
	if networkOperation(fn) {
		add(effectNetwork, "external network I/O is reachable")
		return nil
	}
	if fn.Pkg() != nil {
		switch fn.Pkg().Path() {
		case "os/exec":
			switch fn.Name() {
			case "Start", "Run", "Output", "CombinedOutput", "Wait":
				add(effectProcess, "an external process is reachable")
				return nil
			}
		case "os":
			if fn.Name() == "StartProcess" {
				add(effectProcess, "an external process is reachable")
				return nil
			}
		case "runtime":
			if fn.Name() == "SetFinalizer" || fn.Name() == "AddCleanup" {
				if fn.Name() == "SetFinalizer" && len(call.Args) == 2 && c.pass.TypesInfo.Types[c.resolve(call.Args[1])].IsNil() {
					return nil
				}
				add(effectOutsideCallback, "a callback is registered to run outside the bubble")
				return nil
			}
		case "testing":
			if fn.Name() == "Parallel" || fn.Name() == "Deadline" {
				add(effectTesting, "this testing API is unavailable on the bubble's *testing.T")
				return nil
			}
		case "testing/synctest":
			if fn.Name() == "Test" || fn.Name() == "Run" {
				add(effectTesting, "this path already starts a synctest bubble and cannot be nested")
				return nil
			}
		case "sync":
			if fn.Name() == "Lock" || fn.Name() == "RLock" {
				add(effectMutex, "mutex contention and progress have not been established")
				return nil
			}
			add(effectLifecycle, "the synchronization object's bubble ownership has not been established")
		}
	}
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		if _, ok := recv.Type().Underlying().(*types.Interface); ok {
			add(effectInterface, "the interface call target is unknown")
			return nil
		}
	}
	if clockOperation(fn) {
		add(effectClock, "clock reads need an assertion-ordering review")
	}
	if knownMigrationCall(fn) {
		if !c.knownMigrationInvocation(fn, call) {
			add(effectFormatting, "formatting may invoke an unmodeled String or Error method")
		}
		return nil
	}
	if fact := c.importMigrationFact(fn); fact != nil {
		c.mergeMigrationFact(body, fn, call, fact)
		return fact
	}
	add(effectOpaqueCall, "the callee has no usable effect summary")
	return nil
}

func migrationTimerOperation(fn *types.Func) bool {
	if fn.Pkg() != nil && fn.Pkg().Path() == "time" {
		switch fn.Name() {
		case "Sleep", "After", "AfterFunc", "NewTimer", "NewTicker", "Tick":
			return true
		}
	}
	return false
}

type migrationEffectEdge struct {
	caller      *ast.BlockStmt
	frame       migrationFrame
	conditional bool
}

func (c *checker) propagateMigrationReasons(edges map[*ast.BlockStmt][]migrationEffectEdge) {
	pending := make([]*ast.BlockStmt, 0, len(c.effects.reasons))
	queued := make(map[*ast.BlockStmt]bool)
	for body := range c.effects.reasons {
		pending = append(pending, body)
		queued[body] = true
	}
	for len(pending) != 0 {
		body := pending[0]
		pending = pending[1:]
		queued[body] = false
		for _, edge := range edges[body] {
			if c.effects.reasons[edge.caller] == nil {
				c.effects.reasons[edge.caller] = make(migrationReasonSet)
			}
			changed := false
			for _, reason := range c.effects.reasons[body].ordered() {
				reason.Trace = append([]migrationFrame{edge.frame}, reason.Trace...)
				reason.Conditional = reason.Conditional || edge.conditional
				changed = c.effects.reasons[edge.caller].add(reason) || changed
			}
			if changed && !queued[edge.caller] {
				pending = append(pending, edge.caller)
				queued[edge.caller] = true
			}
		}
	}
}
