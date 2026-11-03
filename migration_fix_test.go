package bubblelint

import (
	"reflect"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestMigrationSuggestedFixes(t *testing.T) {
	for _, tc := range []struct {
		pkg   string
		st100 int
		st101 int
	}{
		{"newimport", 5, 0},
		{"alias", 1, 0},
		{"dot", 1, 0},
		{"blank", 1, 0},
		{"collision", 1, 0},
		{"timeout", 1, 12},
	} {
		t.Run(tc.pkg, func(t *testing.T) {
			results := analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), New(&Options{CheckMisuse: true, Suggest: true}), "migrationfix/"+tc.pkg)
			fixed := map[string]int{}
			var sleepFix *analysis.SuggestedFix
			var timeoutFixes []analysis.SuggestedFix
			for _, result := range results {
				for _, diagnostic := range result.Diagnostics {
					if len(diagnostic.SuggestedFixes) != 1 || (diagnostic.Category != "ST100" && diagnostic.Category != "ST101") {
						t.Errorf("unexpected migration fix: %+v", diagnostic)
						continue
					}
					fixed[diagnostic.Category]++
					if diagnostic.Category == "ST100" {
						sleepFix = &diagnostic.SuggestedFixes[0]
					} else {
						timeoutFixes = append(timeoutFixes, diagnostic.SuggestedFixes[0])
					}
				}
			}
			if fixed["ST100"] != tc.st100 || fixed["ST101"] != tc.st101 {
				t.Errorf("got %d ST100 and %d ST101 fixable diagnostics, want %d and %d", fixed["ST100"], fixed["ST101"], tc.st100, tc.st101)
			}
			if tc.pkg == "timeout" && sleepFix != nil {
				matching := 0
				for _, fix := range timeoutFixes {
					if reflect.DeepEqual(*sleepFix, fix) {
						matching++
					}
				}
				if matching != 1 {
					t.Errorf("the ST100 and ST101 in TestSelect must share identical fixes; got %d matching timeout fixes", matching)
				}
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
	if diagnostics != 48 {
		t.Errorf("got %d unsupported migration diagnostics, want 48", diagnostics)
	}
}
