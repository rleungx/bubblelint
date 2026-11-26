package integration

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/astutil"
)

func TestCLIMigrationEffects(t *testing.T) {
	if *cliBinary == "" {
		t.Skip("run make integration to test the standalone binary")
	}
	root := repositoryRoot(t)
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module migrationeffects\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{
		"migrationeffects/effects_test.go":     "effects_test.go",
		"migrationeffects/worker/worker.go":    "worker/worker.go",
		"migrationeffects/worker/generated.go": "worker/generated.go",
		"migrationeffects/relay/relay.go":      "relay/relay.go",
		"migrationexplain/explain_test.go":     "explain/explain_test.go",
	} {
		data, err := os.ReadFile(filepath.Join(root, "testdata", "src", from))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(module, to)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, exit := runCommand(t, module, *cliBinary, "-checks=ST102", ".")
	if exit != 3 || strings.Count(stdout+stderr, "ST102:") != 5 || strings.Contains(stdout+stderr, "migration blocked:") {
		t.Fatalf("cross-package summaries did not preserve the five local timer candidates: exit=%d\n%s%s", exit, stdout, stderr)
	}
	stdout, stderr, exit = runCommand(t, module, *cliBinary, "-checks=ST102", "-explain-migration", ".")
	if exit != 3 || !strings.Contains(stdout+stderr, "migrationeffects/worker.GeneratedFetch -> migrationeffects/worker.Fetch -> net.Dial") {
		t.Fatalf("generated code lost its network effects: exit=%d\n%s%s", exit, stdout, stderr)
	}
	stdout, stderr, exit = runCommand(t, module, *cliBinary, "-checks=ST102", "./explain")
	if exit != 0 || stdout+stderr != "" {
		t.Fatalf("explanations must be opt-in: exit=%d\n%s%s", exit, stdout, stderr)
	}
	stdout, stderr, exit = runCommand(t, module, *cliBinary, "-checks=ST102", "-explain-migration", "./explain")
	output := stdout + stderr
	if exit != 3 || strings.Count(output, "migration blocked:") != 1 || strings.Count(output, "migration unproven:") != 4 || strings.Contains(output, "ST102:") {
		t.Fatalf("incorrect migration classifications: exit=%d\n%s", exit, output)
	}
	stdout, stderr, exit = runCommand(t, module, *cliBinary, "-checks=ST102", "-explain-migration", "-json", "./explain")
	if exit != 0 {
		t.Fatalf("cannot read migration JSON: exit=%d\n%s%s", exit, stdout, stderr)
	}
	var result map[string]map[string][]struct {
		Category string
		Message  string
		Related  []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode migration JSON: %v\n%s%s", err, stdout, stderr)
	}
	count := 0
	for _, analyzers := range result {
		for _, diagnostic := range analyzers["bubblelint"] {
			count++
			if diagnostic.Category != "migration" || len(diagnostic.Related) == 0 {
				t.Fatalf("JSON lost the classification or evidence: %+v", diagnostic)
			}
		}
	}
	if count != 5 {
		t.Fatalf("got %d migration explanations, want 5", count)
	}
	runProposedMigrations(t, module, "effects_test.go", []string{"TestGeneratedGetter"})
}

func TestCLIMigrationContext(t *testing.T) {
	if *cliBinary == "" {
		t.Skip("run make integration to test the standalone binary")
	}
	root, module := repositoryRoot(t), t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module migrationcontext\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"context_test.go", "worker/worker.go"} {
		data, err := os.ReadFile(filepath.Join(root, "testdata", "src", "migrationcontext", relative))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(module, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, exit := runCommand(t, module, *cliBinary, "-checks=ST102", "-explain-migration", "-json", ".")
	if exit != 0 {
		t.Fatalf("context scan failed: exit=%d\n%s%s", exit, stdout, stderr)
	}
	var result map[string]map[string][]struct {
		Category string
		Message  string
		Related  []struct{ Message string }
		Fixes    []json.RawMessage `json:"suggested_fixes"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, analyzers := range result {
		for _, diagnostic := range analyzers["bubblelint"] {
			key := diagnostic.Category
			if key == "migration" {
				key = "unproven"
				if strings.HasPrefix(diagnostic.Message, "migration blocked:") {
					key = "blocked"
				}
			}
			counts[key]++
			if len(diagnostic.Fixes) != 0 {
				t.Fatalf("review or explanation offered a fix: %+v", diagnostic)
			}
		}
	}
	if counts["ST102"] != 6 || counts["blocked"] != 4 || counts["unproven"] != 4 {
		t.Fatalf("incorrect context assessments: %v\n%s", counts, stdout)
	}
	positive := []string{"TestBoolFalse", "TestNilBackend", "TestPureCallback", "TestReceiver", "TestConstructor", "TestRecursiveBaseCase"}
	runProposedMigrations(t, module, "context_test.go", positive)
}

func TestCLIMigrationSSA(t *testing.T) {
	if *cliBinary == "" {
		t.Skip("run make integration to test the standalone binary")
	}
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module migrationssa\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"state_test.go", "opaque/opaque.go"} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "testdata", "src", "migrationssa", relative))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(module, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, exit := runCommand(t, module, *cliBinary, "-checks=ST102", "-explain-migration", "-json", ".")
	if exit != 0 {
		t.Fatalf("SSA scan failed: exit=%d\n%s%s", exit, stdout, stderr)
	}
	var result map[string]map[string][]struct {
		Category string
		Message  string
		Related  []struct{ Message string }
		Fixes    []json.RawMessage `json:"suggested_fixes"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	proof := false
	for _, analyzers := range result {
		for _, diagnostic := range analyzers["bubblelint"] {
			key := diagnostic.Category
			if key == "migration" {
				key = "unproven"
				if strings.HasPrefix(diagnostic.Message, "migration blocked:") {
					key = "blocked"
				}
			}
			counts[key]++
			for _, related := range diagnostic.Related {
				proof = proof || strings.HasPrefix(related.Message, "test-specific branch condition is ")
			}
			if len(diagnostic.Fixes) != 0 {
				t.Fatalf("review or explanation offered a fix: %+v", diagnostic)
			}
		}
	}
	if counts["ST102"] != 3 || counts["blocked"] != 5 || counts["unproven"] != 16 || !proof {
		t.Fatalf("incorrect SSA assessments: %v, branch evidence=%v\n%s", counts, proof, stdout)
	}
	runProposedMigrations(t, module, "state_test.go", []string{"TestZeroAtomic", "TestConcreteInterface", "TestFreshSubtest/local"})
}

func TestCLIMigrationProtocol(t *testing.T) {
	if *cliBinary == "" {
		t.Skip("run make integration to test the standalone binary")
	}
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module migrationreview\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review_test.go", "boundaries_test.go", "protocols_test.go"} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "testdata", "src", "migrationreview", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(module, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runProposedMigrations(t, module, "protocols_test.go", []string{"TestOpaqueGate"})
}

// Exercise proposed migrations in real bubbles. Negative fixtures are compiled
// but excluded from execution.
func runProposedMigrations(t *testing.T, module, filename string, positive []string) {
	t.Helper()
	fset := token.NewFileSet()
	target := filepath.Join(module, filename)
	file, err := parser.ParseFile(fset, target, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	astutil.AddImport(fset, file, "testing/synctest")
	wrapped := 0
	wrap := func(signature *ast.FuncType, body *ast.BlockStmt) *ast.BlockStmt {
		return &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("synctest"), Sel: ast.NewIdent("Test")},
			Args: []ast.Expr{ast.NewIdent("t"), &ast.FuncLit{Type: signature, Body: body}},
		}}}}
	}
	var testNames []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, name := range positive {
			parent, subtest, nested := strings.Cut(name, "/")
			if fn.Name.Name != parent {
				continue
			}
			testNames = append(testNames, parent)
			if !nested {
				fn.Body = wrap(fn.Type, fn.Body)
				wrapped++
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Run" {
					return true
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				label, err := strconv.Unquote(literal.Value)
				callback, ok := call.Args[1].(*ast.FuncLit)
				if err == nil && ok && label == subtest {
					callback.Body = wrap(callback.Type, callback.Body)
					wrapped++
					return false
				}
				return true
			})
		}
	}
	if wrapped != len(positive) {
		t.Fatalf("wrapped %d tests, want %d", wrapped, len(positive))
	}
	var source bytes.Buffer
	if err := format.Node(&source, fset, file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	pattern := "^(" + strings.Join(testNames, "|") + ")$"
	for _, args := range [][]string{{"test", ".", "-run", pattern, "-count=25"}, {"test", "-race", ".", "-run", pattern, "-count=5"}} {
		stdout, stderr, exit := runCommand(t, module, "go", args...)
		if exit != 0 {
			t.Fatalf("migrated fixtures failed: exit=%d\n%s%s", exit, stdout, stderr)
		}
	}
}
