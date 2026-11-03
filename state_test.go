package bubblelint

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestCheckerMemoizesRelevantBindings(t *testing.T) {
	const levels = 10
	var source strings.Builder
	source.WriteString(`package statefixture
import (
	"testing"
	"testing/synctest"
)
func TestGraph(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		level0a(bt, true)
		level0b(bt, false)
	})
}
func namedCleanup() { synctest.Wait() }
`)
	for level := 0; level < levels; level++ {
		for _, branch := range []string{"a", "b"} {
			fmt.Fprintf(&source, "func level%d%s(t *testing.T, left bool) {\n", level, branch)
			source.WriteString("t.Cleanup(namedCleanup)\n")
			if level+1 < levels {
				fmt.Fprintf(&source, "if left { level%da(t, left) } else { level%db(t, left) }\n", level+1, level+1)
			} else {
				source.WriteString("t.Parallel() // want \"ST003:\"\n")
			}
			source.WriteString("}\n")
		}
	}

	for _, c := range runStateFixture(t, source.String()) {
		// There is one context for every helper, the root, its bubble
		// callback, and the named cleanup. Retaining unrelated ancestor
		// bindings instead produces exponentially many contexts in this DAG.
		if got, want := len(c.visited), 2*levels+3; got != want {
			t.Errorf("analyzed %d contexts, want %d for %d helper levels", got, want, levels)
		}
		if got := namedFunctionVisits(c, "namedCleanup"); got != 1 {
			t.Errorf("named callback analyzed in %d contexts, want 1", got)
		}
	}
}

func TestCheckerPreservesCapturedBindings(t *testing.T) {
	const source = `package statefixture
import (
	"testing"
	"testing/synctest"
)
func TestCapturedBindings(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		capturedHelper(t)
		capturedHelper(bt)
		t.Run("subtest", func(st *testing.T) {
			capturedHelper(st)
		})
	})
}
func capturedHelper(t *testing.T) {
	alias := t
	closure := func() {
		nested := func() {
			alias.Parallel() // want "ST003:"
		}
		nested()
		alias.Cleanup(func() {
			synctest.Wait() // want "ST001:"
		})
	}
	closure()
}
`
	for _, c := range runStateFixture(t, source) {
		// All three calls execute in a bubble, but the captured T is either
		// owned outside it, the special T, or an ordinary T owned inside it.
		if got := namedFunctionVisits(c, "capturedHelper"); got != 3 {
			t.Errorf("captured helper analyzed in %d contexts, want 3 distinct T bindings", got)
		}
	}
}

func runStateFixture(t *testing.T, source string) []*checker {
	t.Helper()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"statefixture/state_test.go": source,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	a := New(nil)
	a.ResultType = reflect.TypeOf((*checker)(nil))
	a.Run = func(pass *analysis.Pass) (any, error) {
		c := newChecker(pass, Options{CheckMisuse: true})
		c.run()
		return c, nil
	}
	var checkers []*checker
	for _, result := range analysistest.Run(t, dir, a, "statefixture") {
		c, ok := result.Result.(*checker)
		if !ok {
			t.Fatalf("unexpected analyzer result %T", result.Result)
		}
		// The loader also returns the package without tests and its test
		// main. Only the variant containing TestGraph/TestCapturedBindings
		// has test roots for this analyzer.
		if len(c.roots) == 0 {
			continue
		}
		checkers = append(checkers, c)
	}
	if len(checkers) == 0 {
		t.Fatal("fixture was not analyzed")
	}
	return checkers
}

func namedFunctionVisits(c *checker, name string) int {
	for obj, fn := range c.functions {
		if obj.Name() != name {
			continue
		}
		count := 0
		for key := range c.visited {
			if key.body == fn.body {
				count++
			}
		}
		return count
	}
	return 0
}
