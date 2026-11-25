package bubblelint

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestMigrationStandardLibraryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, filename, source string
		module                 *analysis.Module
		want                   bool
	}{
		{"toolchain", filepath.Join(build.Default.GOROOT, "src", "net", "boundary.go"), "package net", nil, true},
		{"empty-module", filepath.Join(build.Default.GOROOT, "src", "net", "boundary.go"), "package net", &analysis.Module{}, true},
		{"gopath", filepath.Join(t.TempDir(), "helper.go"), "package helper", nil, false},
		{"module", filepath.Join(build.Default.GOROOT, "src", "example", "helper.go"), "package helper", &analysis.Module{Path: "example"}, false},
		{"line-directive", filepath.Join(t.TempDir(), "helper.go"), "//line " + filepath.Join(build.Default.GOROOT, "src", "net", "boundary.go") + ":1\npackage helper", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, tc.filename, tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			pass := &analysis.Pass{Fset: fset, Files: []*ast.File{file}, Module: tc.module}
			if got := migrationStandardLibrarySource(pass); got != tc.want {
				t.Fatalf("standard library boundary=%v, want %v", got, tc.want)
			}
		})
	}
}
