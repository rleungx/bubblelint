package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationPoolEffects(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST100", "ST101", "ST102"}, ExplainMigration: true,
	}), "migrationpool")
	suggestions, unproven, blocked := 0, 0, 0
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			switch diagnostic.Category {
			case "ST100", "ST101", "ST102":
				suggestions++
			case "migration":
				if strings.HasPrefix(diagnostic.Message, "migration blocked:") {
					blocked++
				} else {
					unproven++
				}
				for _, related := range diagnostic.Related {
					if strings.Contains(related.Message, "synchronization object's bubble ownership") {
						t.Fatalf("pool operation needs its public API model: %+v", diagnostic)
					}
				}
			}
		}
	}
	if suggestions != 5 || unproven != 5 || blocked != 1 {
		t.Fatalf("got %d suggestions, %d unproven, %d blocked; want 5, 5, 1", suggestions, unproven, blocked)
	}
}
