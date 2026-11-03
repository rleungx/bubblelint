package bubblelint

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationSuggestedFixes(t *testing.T) {
	for _, tc := range []struct {
		pkg   string
		fixes int
	}{
		{"newimport", 5},
		{"alias", 1},
		{"dot", 1},
		{"blank", 1},
		{"collision", 1},
	} {
		t.Run(tc.pkg, func(t *testing.T) {
			results := analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), New(&Options{CheckMisuse: true, Suggest: true}), "migrationfix/"+tc.pkg)
			fixed := 0
			for _, result := range results {
				for _, diagnostic := range result.Diagnostics {
					if len(diagnostic.SuggestedFixes) == 0 {
						t.Errorf("expected a migration fix: %+v", diagnostic)
						continue
					}
					fixed++
					if diagnostic.Category != "ST100" || len(diagnostic.SuggestedFixes) != 1 {
						t.Errorf("unexpected migration fix: %+v", diagnostic)
					}
				}
			}
			if fixed != tc.fixes {
				t.Errorf("got %d fixable diagnostics, want %d", fixed, tc.fixes)
			}
		})
	}
}

func TestMigrationRejectsUnsupportedTests(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(&Options{CheckMisuse: true, Suggest: true}), "migrationnofix")
	diagnostics := 0
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			diagnostics++
			if len(diagnostic.SuggestedFixes) != 0 {
				t.Errorf("unsafe migration fix: %+v", diagnostic)
			}
		}
	}
	if diagnostics == 0 {
		t.Fatal("unsupported migration fixtures did not produce suggestions")
	}
}
