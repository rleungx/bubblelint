package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationInterfaceDecisions(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST100", "ST101", "ST102"}, ExplainMigration: true,
	}), "migrationinterfaces")
	counts := make(map[string]int)
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			category := diagnostic.Category
			if category == "migration" {
				category = "unproven"
				if strings.HasPrefix(diagnostic.Message, "migration blocked:") {
					category = "blocked"
				}
			}
			counts[category]++
		}
	}
	if counts["ST102"] != 8 || counts["blocked"] != 6 || counts["unproven"] != 13 {
		t.Fatalf("incorrect interface decisions: %v", counts)
	}
}
