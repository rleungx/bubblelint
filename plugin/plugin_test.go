package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
	"github.com/rleungx/bubblelint"
	"golang.org/x/tools/go/analysis"
)

func TestPlugin(t *testing.T) {
	constructor, err := register.GetPlugin("bubblelint")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		raw    any
		checks []string
	}{
		{"defaults", nil, nil},
		{"empty-settings", map[string]any{}, nil},
		{"null-checks", map[string]any{"checks": nil}, nil},
		{"no-checks", map[string]any{"checks": []string{}}, []string{}},
		{"all", map[string]any{"checks": []string{"all"}}, []string{"all"}},
		{"one-check", map[string]any{"checks": []string{"ST003"}}, []string{"ST003"}},
		{"exclusions", map[string]any{"checks": []string{"all", "-ST101"}}, []string{"all", "-ST101"}},
		{"only-exclusions", map[string]any{"checks": []string{"-ST101"}}, []string{"-ST101"}},
		{"re-enable", map[string]any{"checks": []string{"all", "-ST003", "ST003"}}, []string{"all", "-ST003", "ST003"}},
		{"reset", map[string]any{"checks": []string{"all", "-all", "ST100"}}, []string{"all", "-all", "ST100"}},
		{"trim-space", map[string]any{"checks": []string{" all ", " -ST101 "}}, []string{"all", "-ST101"}},
		{"decoded-yaml-list", map[string]any{"checks": []any{"ST001", "ST002"}}, []string{"ST001", "ST002"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := constructor(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if p.GetLoadMode() != register.LoadModeTypesInfo {
				t.Fatal("plugin must load type information")
			}
			a := buildAnalyzer(t, p)
			if a.Name != "bubblelint" {
				t.Fatalf("unexpected analyzer name %q", a.Name)
			}
			if a.Flags.Lookup("suggest") != nil || a.Flags.Lookup("check-misuse") != nil {
				t.Fatal("legacy flags must not be registered")
			}
			want := bubblelint.New(&bubblelint.Options{Checks: tc.checks}).Flags.Lookup("checks").Value.String()
			if got := a.Flags.Lookup("checks").Value.String(); got != want {
				t.Fatalf("plugin checks = %q, command-line analyzer checks = %q", got, want)
			}
		})
	}
}

func TestPluginRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"unknown-key", map[string]any{"check": []string{"all"}}},
		{"legacy-suggest", map[string]any{"suggest": true}},
		{"legacy-check-misuse", map[string]any{"check-misuse": false}},
		{"legacy-with-checks", map[string]any{"checks": []string{"all"}, "suggest": false}},
		{"unknown-rule", map[string]any{"checks": []string{"ST999"}}},
		{"unknown-exclusion", map[string]any{"checks": []string{"all", "-ST999"}}},
		{"wrong-case", map[string]any{"checks": []string{"st003"}}},
		{"wildcard", map[string]any{"checks": []string{"ST*"}}},
		{"comma-in-element", map[string]any{"checks": []string{"ST001,ST003"}}},
		{"empty-element", map[string]any{"checks": []string{""}}},
		{"blank-element", map[string]any{"checks": []string{" "}}},
		{"missing-excluded-rule", map[string]any{"checks": []string{"-"}}},
		{"double-exclusion", map[string]any{"checks": []string{"--ST003"}}},
		{"embedded-space", map[string]any{"checks": []string{"- ST003"}}},
		{"string-instead-of-list", map[string]any{"checks": "all"}},
		{"boolean-instead-of-list", map[string]any{"checks": true}},
		{"number-instead-of-list", map[string]any{"checks": 1}},
		{"number-element", map[string]any{"checks": []any{"all", 1}}},
		{"boolean-element", map[string]any{"checks": []any{true}}},
		{"null-element", map[string]any{"checks": []any{nil}}},
		{"top-level-string", "all"},
		{"top-level-list", []string{"all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.raw); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestPluginAnalyzersAreIndependent(t *testing.T) {
	checks := []string{"ST003"}
	p, err := New(map[string]any{"checks": checks})
	if err != nil {
		t.Fatal(err)
	}
	checks[0] = "ST100"
	first := buildAnalyzer(t, p)
	second := buildAnalyzer(t, p)
	if err := first.Flags.Set("checks", "ST101"); err != nil {
		t.Fatal(err)
	}
	third := buildAnalyzer(t, p)
	for _, a := range []*analysis.Analyzer{second, third} {
		if got := a.Flags.Lookup("checks").Value.String(); got != "ST003" {
			t.Errorf("another analyzer or the input slice changed checks to %q", got)
		}
	}
}

func buildAnalyzer(t *testing.T, p register.LinterPlugin) *analysis.Analyzer {
	t.Helper()
	analyzers, err := p.BuildAnalyzers()
	if err != nil || len(analyzers) != 1 {
		t.Fatalf("BuildAnalyzers: %v, %v", analyzers, err)
	}
	return analyzers[0]
}
