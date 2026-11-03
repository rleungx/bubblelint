package bubblelint

import (
	"strings"
	"testing"
)

func TestCheckerBoundsConditionalDefers(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package statefixture
import (
	"testing"
	"testing/synctest"
)
func deferredWait() { synctest.Wait() } // want "ST001:"
func deferredStart() { go func() {}() }
func TestConditionalDefers(t *testing.T) {
`)
	// Each pair independently registers either, both, or neither defer. A
	// deferred-stack key would enumerate exponentially many combinations.
	for range 40 {
		source.WriteString("if testing.Short() { defer deferredWait() }\n")
		source.WriteString("if testing.Short() { defer deferredStart() }\n")
	}
	source.WriteString("}\n")
	for _, c := range runStateFixture(t, source.String()) {
		for _, name := range []string{"deferredWait", "deferredStart"} {
			if got := namedFunctionVisits(c, name); got != 2 {
				t.Errorf("analyzed %s in %d contexts, want both async states", name, got)
			}
		}
	}
}
