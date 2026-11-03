package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

func TestPlugin(t *testing.T) {
	constructor, err := register.GetPlugin("bubblelint")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		raw             any
		misuse, suggest string
		wantError       bool
	}{
		{"defaults", nil, "true", "false", false},
		{"suggestions", map[string]any{"suggest": true}, "true", "true", false},
		{"only-suggestions", map[string]any{"suggest": true, "check-misuse": false}, "false", "true", false},
		{"all-disabled", map[string]any{"suggest": false, "check-misuse": false}, "false", "false", false},
		{"unknown-key", map[string]any{"sugest": true}, "", "", true},
		{"wrong-type", map[string]any{"suggest": "true"}, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := constructor(tc.raw)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.GetLoadMode() != register.LoadModeTypesInfo {
				t.Fatal("plugin must load type information")
			}
			analyzers, err := p.BuildAnalyzers()
			if err != nil || len(analyzers) != 1 {
				t.Fatalf("BuildAnalyzers: %v, %v", analyzers, err)
			}
			a := analyzers[0]
			if a.Name != "bubblelint" || a.Flags.Lookup("check-misuse").Value.String() != tc.misuse || a.Flags.Lookup("suggest").Value.String() != tc.suggest {
				t.Fatal("unexpected analyzer configuration")
			}
		})
	}
}
