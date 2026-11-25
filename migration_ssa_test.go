package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationSSAContexts(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{Checks: []string{"ST102"}, ExplainMigration: true}), "migrationssa")
	proofs := 0
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			for _, related := range diagnostic.Related {
				if strings.HasPrefix(related.Message, "test-specific branch condition is ") {
					proofs++
				}
			}
		}
	}
	if proofs == 0 {
		t.Fatal("context refinement produced no branch evidence")
	}
}
