package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationSSAContexts(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{Checks: []string{"ST100", "ST101", "ST102"}, ExplainMigration: true}), "migrationssa")
	proofs := 0
	independent := map[string]bool{"TestIndependentSetup": false, "TestUnstableDispatch": false}
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			entry, branchProof := "", false
			for _, related := range diagnostic.Related {
				if name, ok := strings.CutPrefix(related.Message, "test entry: "); ok {
					if _, check := independent[name]; check {
						entry = name
					}
				}
				if strings.HasPrefix(related.Message, "test-specific branch condition is ") {
					proofs++
					branchProof = true
				}
			}
			if entry != "" {
				if !branchProof || strings.Contains(diagnostic.Message, "external network") {
					t.Fatalf("unrelated setup discarded worker state: %+v", diagnostic)
				}
				independent[entry] = true
			}
		}
	}
	if proofs == 0 {
		t.Fatal("context refinement produced no branch evidence")
	}
	for entry, found := range independent {
		if !found {
			t.Fatalf("missing the assessment for %s", entry)
		}
	}
}
