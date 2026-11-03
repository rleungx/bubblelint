// Package plugin registers bubblelint with golangci-lint's module plugin system.
package plugin

import (
	"github.com/golangci/plugin-module-register/register"
	"github.com/rleungx/bubblelint"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("bubblelint", New)
}

// Settings is decoded strictly so misspelled settings fail configuration.
type Settings struct {
	CheckMisuse *bool `json:"check-misuse"`
	Suggest     *bool `json:"suggest"`
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
	options := bubblelint.Options{CheckMisuse: true, Suggest: true}
	if settings.CheckMisuse != nil {
		options.CheckMisuse = *settings.CheckMisuse
	}
	if settings.Suggest != nil {
		options.Suggest = *settings.Suggest
	}
	return &plugin{options: options}, nil
}

func (p *plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{bubblelint.New(&p.options)}, nil
}

func (*plugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
