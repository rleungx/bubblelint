package bubblelint

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationEffectsAcrossPackages(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(&Options{Checks: []string{"ST102"}}), "migrationeffects")
}

func TestContextualMigrationGlobals(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST102"}, ExplainMigration: true,
	}), "migrationcontextglobals")
}

func TestContextualMigrationMutation(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST102"}, ExplainMigration: true,
	}), "migrationcontextmutation")
}

func TestContextualMigrationInitializers(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST102"}, ExplainMigration: true,
	}), "migrationcontextinitializers")
}

func TestConditionalMigrationEffects(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST102"}, ExplainMigration: true,
	}), "migrationconditional")
}

func TestContextualMigrationEffects(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST102"}, ExplainMigration: true,
	}), "migrationcontext")
	found := false
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			if !strings.Contains(diagnostic.Message, "NestedReturn") {
				continue
			}
			entries := make(map[string]bool)
			for _, related := range diagnostic.Related {
				if strings.HasPrefix(related.Message, "test entry: ") {
					entries[related.Message] = true
				}
			}
			if len(entries) != 2 || !entries["test entry: TestNestedFirst"] || !entries["test entry: TestNestedSecond"] {
				t.Fatalf("helper diagnostic lost its test entries: %+v", diagnostic.Related)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing the shared helper's assessment")
	}
}

func TestMigrationExplanations(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{
		Checks: []string{"ST102"}, ExplainMigration: true,
	}), "migrationexplain")
	count := 0
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			count++
			if diagnostic.Category != "migration" || len(diagnostic.SuggestedFixes) != 0 {
				t.Fatalf("explanation must not be a suggestion or offer a fix: %+v", diagnostic)
			}
			if len(diagnostic.Related) == 0 {
				t.Fatalf("explanation has no evidence locations: %+v", diagnostic)
			}
			if strings.Contains(diagnostic.Message, "relay.Fetch") && !strings.Contains(diagnostic.Message, "worker.Fetch -> net.Dial") {
				t.Fatalf("lost the transitive network witness: %s", diagnostic.Message)
			}
		}
	}
	if count != 5 {
		t.Fatalf("got %d explanations, want 5", count)
	}
}
