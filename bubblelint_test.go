package bubblelint

import (
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *Options
		pkg     string
	}{
		{"misuse", nil, "misuse"},
		{"migration", &Options{Suggest: true}, "migration"},
		{"clean", &Options{CheckMisuse: true, Suggest: true}, "clean"},
		{"disabled", &Options{}, "disabled"},
		{"defaults", nil, "defaults"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analyzer := New(tc.options)
			if err := analysis.Validate([]*analysis.Analyzer{analyzer}); err != nil {
				t.Fatal(err)
			}
			analysistest.Run(t, analysistest.TestData(), analyzer, tc.pkg)
		})
	}
}

func TestIndependentFlags(t *testing.T) {
	first, second := New(nil), New(nil)
	if err := first.Flags.Set("suggest", "true"); err != nil {
		t.Fatal(err)
	}
	if err := first.Flags.Set("check-misuse", "false"); err != nil {
		t.Fatal(err)
	}
	if second.Flags.Lookup("suggest").Value.String() != "false" || second.Flags.Lookup("check-misuse").Value.String() != "true" {
		t.Fatal("analyzers share mutable settings")
	}
}
