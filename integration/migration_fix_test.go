package integration

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const migrationFixSource = `package autofix

import (
	"testing"
	"time"
)

func TestClose(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Second)
		close(done)
	}()
	time.Sleep(time.Second) // keepMigration: preserve this wait.
	time.Sleep(time.Second) // keepMigration: preserve this wait too.
	<-done
}

func TestValues(t *testing.T) {
	answer := make(chan int)
	ready := make(chan bool)
	go func() {
		time.Sleep(time.Second)
		answer <- 42
	}()
	go func() {
		time.Sleep(time.Second)
		ready <- true
	}()
	time.Sleep(time.Second)
	got := <-answer
	ok := <-ready
	if got != 42 || !ok {
		t.Fatalf("got (%d, %t), want (42, true)", got, ok)
	}
}
`

const unsupportedMigrationSource = `
func finish(done chan struct{}) { close(done) }

func TestHelper(t *testing.T) {
	done := make(chan struct{})
	go finish(done)
	time.Sleep(time.Second)
	<-done
}

func TestLeakedWorker(t *testing.T) {
	go func() { time.Sleep(time.Second) }()
	time.Sleep(time.Second)
}

func TestTimeout(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Second)
		close(done)
	}()
	time.Sleep(time.Second)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}
`

func (d autofixDriver) runMigration(t *testing.T, dir string, fix, suggest bool, exclusions string) (string, string, int) {
	t.Helper()
	var args []string
	if d.golangci {
		settings := ""
		if suggest {
			settings = "suggest: true"
		}
		args = []string{"run", "--config", writeConfig(t, settings, exclusions), "--show-stats=false"}
		if fix {
			args = append(args, "--fix")
		}
	} else {
		if suggest {
			args = append(args, "-suggest")
		}
		if fix {
			args = append(args, "-fix")
		}
	}
	return runCommand(t, dir, d.binary, append(args, "./...")...)
}

func TestMigrationFix(t *testing.T) {
	for _, driver := range []autofixDriver{
		{name: "CLI", binary: *cliBinary},
		{name: "golangci-lint", binary: *golangciBinary, golangci: true},
	} {
		t.Run(driver.name, func(t *testing.T) {
			if driver.binary == "" {
				t.Skip("run make integration to test migration fixes")
			}
			var err error
			driver.binary, err = filepath.Abs(driver.binary)
			if err != nil {
				t.Fatal(err)
			}
			diagnosticExit := 3
			remainingFixExit := 0
			if driver.golangci {
				diagnosticExit, remainingFixExit = 1, 1
			}

			for _, alias := range []string{"", "st"} {
				name := "add-import"
				source := migrationFixSource
				if alias != "" {
					name = "reuse-import-alias"
					source = strings.Replace(source, `"testing"`, "\"testing\"\n\tst \"testing/synctest\"", 1)
					source += "\nfunc existingBubble(t *testing.T) { st.Test(t, func(t *testing.T) {}) }\n"
				}
				t.Run(name, func(t *testing.T) {
					dir, path := writeAutofixModule(t, source)
					original := readAutofixFile(t, path)
					if !driver.golangci && alias == "" {
						stdout, stderr, exit := runCommand(t, dir, "go", "test", "-race", "-count=1", "-timeout=30s", "./...")
						checkAutofixExit(t, exit, 0, stdout, stderr)
					}
					stdout, stderr, exit := driver.runMigration(t, dir, false, true, "")
					checkAutofixExit(t, exit, diagnosticExit, stdout, stderr)
					if !strings.Contains(stdout+stderr, "ST100:") {
						t.Fatalf("missing migration suggestion\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
					}
					checkAutofixUnchanged(t, path, original)

					stdout, stderr, exit = driver.runMigration(t, dir, true, true, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					checkMigratedFunctions(t, path, original, []string{"TestClose", "TestValues"}, alias)
					fixed := readAutofixFile(t, path)
					stdout, stderr, exit = runCommand(t, dir, "go", "test", "-race", "-count=1", "-timeout=30s", "./...")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					stdout, stderr, exit = driver.runMigration(t, dir, true, true, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					checkAutofixUnchanged(t, path, fixed)
					stdout, stderr, exit = driver.runMigration(t, dir, false, true, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
				})
			}

			t.Run("suggest-is-opt-in", func(t *testing.T) {
				dir, path := writeAutofixModule(t, migrationFixSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.runMigration(t, dir, true, false, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkAutofixUnchanged(t, path, original)
			})

			t.Run("preserve-unsupported-tests", func(t *testing.T) {
				dir, path := writeAutofixModule(t, migrationFixSource+unsupportedMigrationSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.runMigration(t, dir, true, true, "")
				checkAutofixExit(t, exit, remainingFixExit, stdout, stderr)
				checkMigratedFunctions(t, path, original, []string{"TestClose", "TestValues"}, "")
				fixed := readAutofixFile(t, path)
				stdout, stderr, exit = driver.runMigration(t, dir, false, true, "")
				checkAutofixExit(t, exit, diagnosticExit, stdout, stderr)
				for _, code := range []string{"ST100:", "ST101:"} {
					if !strings.Contains(stdout+stderr, code) {
						t.Errorf("missing remaining suggestion %s\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
					}
				}
				stdout, stderr, exit = driver.runMigration(t, dir, true, true, "")
				checkAutofixExit(t, exit, remainingFixExit, stdout, stderr)
				checkAutofixUnchanged(t, path, fixed)
			})

			if driver.golangci {
				for _, tc := range []struct{ name, source, exclusions string }{
					{
						name:   "nolint-one-test",
						source: strings.Replace(migrationFixSource, "func TestClose", "//nolint:bubblelint // Keep this test unchanged.\nfunc TestClose", 1),
					},
					{
						name: "exclude-one-test", source: migrationFixSource,
						exclusions: "    rules:\n      - path: 'fix_test\\.go$'\n        linters: [bubblelint]\n        source: 'keepMigration'\n",
					},
				} {
					t.Run(tc.name, func(t *testing.T) {
						dir, path := writeAutofixModule(t, tc.source)
						original := readAutofixFile(t, path)
						stdout, stderr, exit := driver.runMigration(t, dir, true, true, tc.exclusions)
						checkAutofixExit(t, exit, 0, stdout, stderr)
						checkMigratedFunctions(t, path, original, []string{"TestValues"}, "")
						fixed := readAutofixFile(t, path)
						stdout, stderr, exit = driver.runMigration(t, dir, true, true, tc.exclusions)
						checkAutofixExit(t, exit, 0, stdout, stderr)
						checkAutofixUnchanged(t, path, fixed)
					})
				}
			}
		})
	}
}

// Check the complete original bodies, including assertions and every Sleep,
// rather than accepting a wrapper that silently drops or changes statements.
func checkMigratedFunctions(t *testing.T, path string, original []byte, migrated []string, alias string) {
	t.Helper()
	parse := func(source []byte) (*token.FileSet, *ast.File) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		return fset, file
	}
	nodeText := func(fset *token.FileSet, node ast.Node) string {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, node); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	fixed := readAutofixFile(t, path)
	beforeSet, before := parse(original)
	afterSet, after := parse(fixed)
	importCount := 0
	if alias == "" {
		alias = "synctest"
	}
	for _, spec := range after.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if importPath == "testing/synctest" {
			importCount++
			gotAlias := "synctest"
			if spec.Name != nil {
				gotAlias = spec.Name.Name
			}
			if gotAlias != alias {
				t.Fatalf("synctest import alias %q, want %q", gotAlias, alias)
			}
		}
	}
	if importCount != 1 {
		t.Fatalf("got %d synctest imports, want one\n%s", importCount, fixed)
	}
	wantMigrated := make(map[string]bool, len(migrated))
	for _, name := range migrated {
		wantMigrated[name] = true
	}
	beforeFunctions := map[string]*ast.FuncDecl{}
	for _, decl := range before.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			beforeFunctions[fn.Name.Name] = fn
		}
	}
	for _, decl := range after.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		originalFn, ok := beforeFunctions[fn.Name.Name]
		if !ok {
			t.Fatalf("unexpected added function %s", fn.Name.Name)
		}
		delete(beforeFunctions, fn.Name.Name)
		body := fn.Body
		if wantMigrated[fn.Name.Name] {
			delete(wantMigrated, fn.Name.Name)
			if len(body.List) != 1 {
				t.Fatalf("%s: want a single synctest.Test wrapper\n%s", fn.Name.Name, fixed)
			}
			statement, ok := body.List[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("%s: wrapper is not an expression statement", fn.Name.Name)
			}
			call, ok := statement.X.(*ast.CallExpr)
			if !ok || nodeText(afterSet, call.Fun) != alias+".Test" || len(call.Args) != 2 {
				t.Fatalf("%s: invalid synctest.Test wrapper\n%s", fn.Name.Name, fixed)
			}
			callback, ok := call.Args[1].(*ast.FuncLit)
			if !ok || nodeText(afterSet, call.Args[0]) != originalFn.Type.Params.List[0].Names[0].Name ||
				nodeText(afterSet, callback.Type) != nodeText(beforeSet, originalFn.Type) {
				t.Fatalf("%s: wrapper changed the testing.T parameter\n%s", fn.Name.Name, fixed)
			}
			body = callback.Body
		}
		if got, want := nodeText(afterSet, body), nodeText(beforeSet, originalFn.Body); got != want {
			t.Errorf("%s: body changed\ngot:\n%s\nwant:\n%s", fn.Name.Name, got, want)
		}
	}
	if len(beforeFunctions) != 0 || len(wantMigrated) != 0 {
		t.Errorf("missing functions: original=%v, expected migration=%v", beforeFunctions, wantMigrated)
	}
	for _, group := range before.Comments {
		for _, comment := range group.List {
			if !bytes.Contains(fixed, []byte(comment.Text)) {
				t.Errorf("fix removed comment %q", comment.Text)
			}
		}
	}
}
