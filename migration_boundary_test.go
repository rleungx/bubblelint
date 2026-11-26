package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationBoundaries(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST100", "ST101", "ST102"}, ExplainMigration: true,
	}), "migrationboundary")
	count := 0
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Category != "ST102" {
				continue
			}
			start, end, outer := false, false, false
			for _, related := range diagnostic.Related {
				start = start || related.Message == "proposed bubble starts here"
				end = end || related.Message == "proposed bubble ends here"
				outer = outer || strings.HasPrefix(related.Message, "keep this leading defer")
			}
			if !start || !end || !outer || len(diagnostic.SuggestedFixes) != 0 {
				t.Fatalf("incomplete manual migration boundary: %+v", diagnostic)
			}
			count++
		}
	}
	if count != 2 {
		t.Fatalf("got %d scoped candidates, want 2", count)
	}
}
