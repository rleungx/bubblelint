package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	golangciBinary = flag.String("golangci-lint", "", "custom golangci-lint binary to test")
	cliBinary      = flag.String("bubblelint", "", "standalone bubblelint binary to test")
)

type diagnostic struct {
	File         string
	Line, Column int
	Code         string
}

var (
	wantPattern = regexp.MustCompile(`\bwant "(ST[0-9]{3}):`)
	cliPattern  = regexp.MustCompile(`(?m)^(.+):([0-9]+):([0-9]+): (ST[0-9]{3}):`)
)

func TestCLI(t *testing.T) {
	if *cliBinary == "" {
		t.Skip("run make integration to test the standalone binary")
	}
	root := repositoryRoot(t)
	for _, tc := range []struct {
		name  string
		args  []string
		codes []string
	}{
		{"defaults", nil, []string{"ST001", "ST002", "ST003", "ST100", "ST101"}},
		{"misuse-only", []string{"-checks=ST001,ST002,ST003"}, []string{"ST001", "ST002", "ST003"}},
		{"all-enabled", []string{"-checks=all"}, []string{"ST001", "ST002", "ST003", "ST100", "ST101"}},
		{"migration-only", []string{"-checks=ST100,ST101"}, []string{"ST100", "ST101"}},
		{"ST001", []string{"-checks=ST001"}, []string{"ST001"}},
		{"ST002", []string{"-checks=ST002"}, []string{"ST002"}},
		{"ST003", []string{"-checks=ST003"}, []string{"ST003"}},
		{"ST100", []string{"-checks=ST100"}, []string{"ST100"}},
		{"ST101", []string{"-checks=ST101"}, []string{"ST101"}},
		{"exclude-one", []string{"-checks=all,-ST101"}, []string{"ST001", "ST002", "ST003", "ST100"}},
		{"reset-selection", []string{"-checks=all,-all,ST003"}, []string{"ST003"}},
		{"reenable-rule", []string{"-checks=all,-ST003,ST003"}, []string{"ST001", "ST002", "ST003", "ST100", "ST101"}},
		{"exclude-from-empty", []string{"-checks=-ST003"}, nil},
		{"empty-selection", []string{"-checks="}, nil},
		{"all-disabled", []string{"-checks=-all"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := expectedDiagnostics(t, root, "integration_cases", tc.codes)
			args := append(slices.Clone(tc.args), "./testdata/src/integration_cases")
			stdout, stderr, exit := runCommand(t, root, *cliBinary, args...)
			wantExit := 0
			if len(want) > 0 {
				wantExit = 3 // singlechecker uses 3 for reported diagnostics.
			}
			if exit != wantExit {
				t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", exit, wantExit, stdout, stderr)
			}
			var got []diagnostic
			for _, match := range cliPattern.FindAllStringSubmatch(stdout+stderr, -1) {
				line, _ := strconv.Atoi(match[2])
				column, _ := strconv.Atoi(match[3])
				got = append(got, diagnostic{relativePath(t, root, match[1]), line, column, match[4]})
			}
			checkDiagnostics(t, got, want, stdout, stderr)
		})
	}
	for _, tc := range []struct {
		name, arg, message string
	}{
		{"unknown-rule", "-checks=ST999", "ST999"},
		{"empty-element", "-checks=ST001,,ST003", "checks"},
		{"wildcard", "-checks=ST1*", "ST1*"},
		{"malformed-token", "-checks=--ST003", "--ST003"},
		{"removed-suggest", "-suggest=true", "suggest"},
		{"removed-check-misuse", "-check-misuse=false", "check-misuse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid settings must fail before trying to load this missing package.
			stdout, stderr, exit := runCommand(t, root, *cliBinary, tc.arg, "./testdata/src/does-not-exist")
			if exit != 2 || !strings.Contains(stdout+stderr, tc.message) {
				t.Fatalf("expected flag error mentioning %q, got exit %d\nstdout:\n%s\nstderr:\n%s", tc.message, exit, stdout, stderr)
			}
		})
	}
}

func TestGolangciLint(t *testing.T) {
	if *golangciBinary == "" {
		t.Skip("run make integration to test the custom binary")
	}
	root := repositoryRoot(t)
	for _, tc := range []struct {
		name, settings, exclusions, pkg string
		codes                           []string
	}{
		{"defaults", "", "", "integration_cases", []string{"ST001", "ST002", "ST003", "ST100", "ST101"}},
		{"misuse-only", "checks: [ST001, ST002, ST003]", "", "integration_cases", []string{"ST001", "ST002", "ST003"}},
		{"all-enabled", "checks: [all]", "", "integration_cases", []string{"ST001", "ST002", "ST003", "ST100", "ST101"}},
		{"migration-only", "checks: [ST100, ST101]", "", "integration_cases", []string{"ST100", "ST101"}},
		{"ST001", "checks: [ST001]", "", "integration_cases", []string{"ST001"}},
		{"ST002", "checks: [ST002]", "", "integration_cases", []string{"ST002"}},
		{"ST003", "checks: [ST003]", "", "integration_cases", []string{"ST003"}},
		{"ST100", "checks: [ST100]", "", "integration_cases", []string{"ST100"}},
		{"ST101", "checks: [ST101]", "", "integration_cases", []string{"ST101"}},
		{"exclude-one", "checks: [all, '-ST101']", "", "integration_cases", []string{"ST001", "ST002", "ST003", "ST100"}},
		{"reset-selection", "checks: [all, '-all', ST003]", "", "integration_cases", []string{"ST003"}},
		{"reenable-rule", "checks: [all, '-ST003', ST003]", "", "integration_cases", []string{"ST001", "ST002", "ST003", "ST100", "ST101"}},
		{"exclude-from-empty", "checks: ['-ST003']", "", "integration_cases", nil},
		{"empty-selection", "checks: []", "", "integration_cases", nil},
		{"all-disabled", "checks: ['-all']", "", "integration_cases", nil},
		{"misuse-regressions", "", "", "misuse", []string{"ST001", "ST002", "ST003"}},
		{"migration-regressions", "", "", "migration", []string{"ST100", "ST101"}},
		{"clean", "", "", "clean", nil},
		{"nolint", "", "", "integration_nolint", []string{"ST001"}},
		{
			"configured-exclusion", "",
			"    rules:\n      - path: 'integration_cases/.*\\.go$'\n        linters: [bubblelint]\n        text: 'ST001:'\n",
			"integration_cases", []string{"ST002", "ST003", "ST100", "ST101"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := expectedDiagnostics(t, root, tc.pkg, tc.codes)
			config := writeConfig(t, tc.settings, tc.exclusions)
			stdout, stderr, exit := runCommand(t, root, *golangciBinary, "run", "--config", config,
				"--show-stats=false", "--output.json.path", "stdout", "--output.text.path", "", "./testdata/src/"+tc.pkg)
			wantExit := 0
			if len(want) > 0 {
				wantExit = 1
			}
			if exit != wantExit {
				t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", exit, wantExit, stdout, stderr)
			}
			var result struct {
				Issues []struct {
					FromLinter string
					Text       string
					Pos        struct {
						Filename     string
						Line, Column int
					}
				}
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("invalid JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			var got []diagnostic
			for _, issue := range result.Issues {
				if issue.FromLinter != "bubblelint" {
					t.Fatalf("unexpected linter %q: %+v", issue.FromLinter, issue)
				}
				code, _, _ := strings.Cut(issue.Text, ":")
				got = append(got, diagnostic{relativePath(t, root, issue.Pos.Filename), issue.Pos.Line, issue.Pos.Column, code})
			}
			checkDiagnostics(t, got, want, stdout, stderr)
		})
	}
	for _, tc := range []struct {
		name, settings, field string
	}{
		{"unknown-rule", "checks: [ST999]", "ST999"},
		{"empty-element", "checks: [ST001, '', ST003]", "checks"},
		{"wildcard", "checks: ['ST1*']", "ST1*"},
		{"malformed-token", "checks: ['--ST003']", "--ST003"},
		{"wrong-type", "checks: ST003", "checks"},
		{"removed-suggest", "suggest: true", "suggest"},
		{"removed-check-misuse", "check-misuse: false", "check-misuse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := writeConfig(t, tc.settings, "")
			// Invalid settings must fail before trying to load this missing package.
			stdout, stderr, exit := runCommand(t, root, *golangciBinary, "run", "--config", config, "./testdata/src/does-not-exist")
			// Exit 1 means ordinary lint findings, not a rejected configuration.
			if exit < 2 || !strings.Contains(stdout+stderr, tc.field) {
				t.Fatalf("expected configuration error mentioning %q, got exit %d\nstdout:\n%s\nstderr:\n%s", tc.field, exit, stdout, stderr)
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runCommand(t *testing.T, root, binary string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = root
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s %v timed out: %v\nstdout:\n%s\nstderr:\n%s", binary, args, ctx.Err(), &stdout, &stderr)
	}
	exit := 0
	if err != nil {
		status, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("cannot run %s %v: %v\nstdout:\n%s\nstderr:\n%s", binary, args, err, &stdout, &stderr)
		}
		exit = status.ExitCode()
	}
	return stdout.String(), stderr.String(), exit
}

func writeConfig(t *testing.T, settings, exclusions string) string {
	t.Helper()
	if settings != "" {
		settings = "        settings:\n          " + settings + "\n"
	}
	config := `version: "2"
run:
  tests: true
  timeout: 2m
  relative-path-mode: gomod
linters:
  default: none
  enable: [bubblelint]
  settings:
    custom:
      bubblelint:
        type: module
` + settings + `  exclusions:
    presets: []
` + exclusions + `issues:
  max-issues-per-linter: 0
  max-same-issues: 0
  uniq-by-line: false
`
	path := filepath.Join(t.TempDir(), ".golangci.yml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// An annotation selects the outer call on its line, or the outer expression if
// there is no call. Callback arguments must be on their own annotated lines.
// AST positions keep fixture line and column changes out of the test table.
func expectedDiagnostics(t *testing.T, root, pkg string, codes []string) []diagnostic {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "testdata", "src", pkg, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("locate fixture %s: files=%v, error=%v", pkg, files, err)
	}
	var result []diagnostic
	for _, path := range files {
		matched, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			t.Fatalf("evaluate fixture build constraints for %s: %v", path, err)
		}
		if !matched {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		want := make(map[int]string)
		for _, group := range file.Comments {
			for _, comment := range group.List {
				match := wantPattern.FindStringSubmatch(comment.Text)
				if match != nil && slices.Contains(codes, match[1]) {
					want[fset.Position(comment.Pos()).Line] = match[1]
				}
			}
		}
		calls := make(map[int][]ast.Expr)
		expressions := make(map[int][]ast.Expr)
		ast.Inspect(file, func(node ast.Node) bool {
			expr, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			line := fset.Position(expr.Pos()).Line
			if _, ok := want[line]; ok {
				expressions[line] = append(expressions[line], expr)
				if _, ok := expr.(*ast.CallExpr); ok {
					calls[line] = append(calls[line], expr)
				}
			}
			return true
		})
		for line, code := range want {
			candidates := calls[line]
			if len(candidates) == 0 {
				candidates = expressions[line]
			}
			if len(candidates) == 0 {
				t.Fatalf("fixture annotation without an expression at %s:%d", path, line)
			}
			// ast.Inspect visits an outer expression before its children.
			expr := candidates[0]
			for _, other := range candidates[1:] {
				if other.Pos() < expr.Pos() || other.End() > expr.End() {
					t.Fatalf("ambiguous fixture annotation at %s:%d; put the diagnosed expression on its own line", path, line)
				}
			}
			pos := fset.Position(expr.Pos())
			result = append(result, diagnostic{relativePath(t, root, path), line, pos.Column, code})
		}
	}
	for _, code := range codes {
		if !slices.ContainsFunc(result, func(d diagnostic) bool { return d.Code == code }) {
			t.Fatalf("fixture %s has no expectation for %s", pkg, code)
		}
	}
	return result
}

func relativePath(t *testing.T, root, path string) string {
	t.Helper()
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func checkDiagnostics(t *testing.T, got, want []diagnostic, stdout, stderr string) {
	t.Helper()
	less := func(a, b diagnostic) int {
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if a.Column != b.Column {
			return a.Column - b.Column
		}
		return strings.Compare(a.Code, b.Code)
	}
	slices.SortFunc(got, less)
	slices.SortFunc(want, less)
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostics differ\ngot:  %+v\nwant: %+v\nstdout:\n%s\nstderr:\n%s", got, want, stdout, stderr)
	}
}
