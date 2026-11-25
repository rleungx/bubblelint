// Package plugin registers bubblelint with golangci-lint's module plugin system.
package plugin

import (
	"fmt"
	"go/token"
	"strings"

	"github.com/golangci/plugin-module-register/register"
	"github.com/rleungx/bubblelint"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("bubblelint", New)
}

// Settings is decoded strictly so misspelled settings fail configuration.
type Settings struct {
	Checks           []string `json:"checks"`
	ExplainMigration bool     `json:"explain-migration"`
}

type plugin struct {
	options bubblelint.Options
}

// New creates a module plugin with the same defaults as the command-line tool.
func New(raw any) (register.LinterPlugin, error) {
	settings, err := register.DecodeSettings[Settings](raw)
	if err != nil {
		return nil, err
	}
	options := bubblelint.Options{Checks: settings.Checks, ExplainMigration: settings.ExplainMigration}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &plugin{options: options}, nil
}

func (p *plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	analyzer := bubblelint.New(&p.options)
	run := analyzer.Run
	analyzer.Run = func(pass *analysis.Pass) (any, error) {
		wrapped := *pass
		wrapped.Report = func(diagnostic analysis.Diagnostic) {
			pass.Report(inlineRelatedInformation(pass.Fset, diagnostic))
		}
		return run(&wrapped)
	}
	return []*analysis.Analyzer{analyzer}, nil
}

// golangci-lint reports related information as separate issues. Keep evidence
// with its diagnostic so exclusions and automatic fixes apply to it together.
func inlineRelatedInformation(fset *token.FileSet, diagnostic analysis.Diagnostic) analysis.Diagnostic {
	if len(diagnostic.Related) == 0 {
		return diagnostic
	}
	var message strings.Builder
	message.WriteString(diagnostic.Message)
	for _, related := range diagnostic.Related {
		fmt.Fprintf(&message, "\n%s: %s", fset.PositionFor(related.Pos, false), related.Message)
	}
	diagnostic.Message = message.String()
	diagnostic.Related = nil
	return diagnostic
}

func (*plugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
