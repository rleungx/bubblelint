package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationSSAContexts(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{Checks: []string{"ST100", "ST101", "ST102"}, ExplainMigration: true}), "migrationssa")
	proofs := 0
	independentSetup := false
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			setupEntry, branchProof := false, false
			for _, related := range diagnostic.Related {
				setupEntry = setupEntry || related.Message == "test entry: TestIndependentSetup"
				if strings.HasPrefix(related.Message, "test-specific branch condition is ") {
					proofs++
					branchProof = true
				}
			}
			if setupEntry {
				if !branchProof || strings.Contains(diagnostic.Message, "external network") {
					t.Fatalf("unrelated setup discarded worker state: %+v", diagnostic)
				}
				independentSetup = true
			}
		}
	}
	if proofs == 0 {
		t.Fatal("context refinement produced no branch evidence")
	}
	if !independentSetup {
		t.Fatal("missing the assessment with independent loop and recursive setup")
	}
}
