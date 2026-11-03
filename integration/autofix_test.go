package integration

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const autofixSource = `package autofix

import (
	"testing"
	"testing/synctest"
)

func TestFix(t *testing.T) {
	ran := false
	synctest.Test(t, func(bt *testing.T) {
		bt.Parallel() // Keep this comment.
		ran = true
	})
	if !ran {
		t.Fatal("bubble callback did not run")
	}
}
`

type autofixDriver struct {
	name, binary string
	golangci     bool
}

func (d autofixDriver) run(t *testing.T, dir string, fix, disabled bool, exclusions string) (string, string, int) {
	t.Helper()
	var args []string
	if d.golangci {
		settings := ""
		if disabled {
			settings = "check-misuse: false"
		}
		args = []string{"run", "--config", writeConfig(t, settings, exclusions), "--show-stats=false"}
		if fix {
			args = append(args, "--fix")
		}
	} else {
		if fix {
			args = append(args, "-fix")
		}
		if disabled {
			args = append(args, "-check-misuse=false")
		}
	}
	return runCommand(t, dir, d.binary, append(args, "./...")...)
}

func TestAutofix(t *testing.T) {
	for _, driver := range []autofixDriver{
		{name: "CLI", binary: *cliBinary},
		{name: "golangci-lint", binary: *golangciBinary, golangci: true},
	} {
		t.Run(driver.name, func(t *testing.T) {
			if driver.binary == "" {
				t.Skip("run make integration to test automatic fixes")
			}
			var err error
			driver.binary, err = filepath.Abs(driver.binary)
			if err != nil {
				t.Fatal(err)
			}
			diagnosticExit := 3
			if driver.golangci {
				diagnosticExit = 1
			}

			t.Run("apply-and-repeat", func(t *testing.T) {
				dir, path := writeAutofixModule(t, autofixSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.run(t, dir, false, false, "")
				checkAutofixExit(t, exit, diagnosticExit, stdout, stderr)
				if !strings.Contains(stdout+stderr, "ST003:") {
					t.Fatalf("expected ST003 before fixing\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
				}
				checkAutofixUnchanged(t, path, original)

				stdout, stderr, exit = driver.run(t, dir, true, false, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkAutofixSource(t, path, expectedAutofixSource(autofixSource))
				fixed := readAutofixFile(t, path)

				// Execute only the fully repaired fixture. The mixed fixture below
				// deliberately retains invalid calls and must never be executed.
				stdout, stderr, exit = runCommand(t, dir, "go", "test", "-count=1", "./...")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				stdout, stderr, exit = driver.run(t, dir, true, false, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkAutofixUnchanged(t, path, fixed)
				stdout, stderr, exit = driver.run(t, dir, false, false, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
			})

			t.Run("preserve-unsupported-calls", func(t *testing.T) {
				source := strings.Replace(autofixSource, "\t\tran = true", "\t\talias := bt\n\t\talias.Parallel()\n\t\t_, _ = bt.Deadline()\n\t\tran = true", 1)
				dir, path := writeAutofixModule(t, source)
				stdout, stderr, exit := driver.run(t, dir, true, false, "")
				fixExit := 0
				if driver.golangci {
					fixExit = 1
				}
				// singlechecker -fix returns success after applying edits, even
				// when some diagnostics have no fix. golangci-lint reports them.
				checkAutofixExit(t, exit, fixExit, stdout, stderr)
				checkAutofixSource(t, path, expectedAutofixSource(source))
				fixed := readAutofixFile(t, path)
				stdout, stderr, exit = driver.run(t, dir, false, false, "")
				checkAutofixExit(t, exit, diagnosticExit, stdout, stderr)
				for _, method := range []string{"testing.T.Parallel", "testing.T.Deadline"} {
					if !strings.Contains(stdout+stderr, method) {
						t.Errorf("missing remaining diagnostic for %s\nstdout:\n%s\nstderr:\n%s", method, stdout, stderr)
					}
				}
				stdout, stderr, exit = driver.run(t, dir, true, false, "")
				checkAutofixExit(t, exit, fixExit, stdout, stderr)
				checkAutofixUnchanged(t, path, fixed)
			})

			t.Run("disabled-checks", func(t *testing.T) {
				dir, path := writeAutofixModule(t, autofixSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.run(t, dir, true, true, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkAutofixUnchanged(t, path, original)
			})

			t.Run("remapped-source", func(t *testing.T) {
				source := strings.Replace(autofixSource, "\t\tbt.Parallel()", "//line victim_test.go:1\n\t\tbt.Parallel()", 1)
				dir, path := writeAutofixModule(t, source)
				original := readAutofixFile(t, path)
				victimPath := filepath.Join(dir, "victim_test.go")
				// Keep the file long enough that accidentally applying the
				// original file's offsets cannot merely fail as out of bounds.
				victim := []byte("package autofix\n\n// " + strings.Repeat("unchanged ", 100) + "\nconst victim = true\n")
				if err := os.WriteFile(victimPath, victim, 0o600); err != nil {
					t.Fatal(err)
				}
				stdout, stderr, exit := driver.run(t, dir, true, false, "")
				fixExit := 0
				if driver.golangci {
					fixExit = 1
				}
				checkAutofixExit(t, exit, fixExit, stdout, stderr)
				checkAutofixUnchanged(t, path, original)
				checkAutofixUnchanged(t, victimPath, victim)
			})

			if driver.golangci {
				for _, tc := range []struct{ name, source, exclusions string }{
					{
						name:   "nolint",
						source: strings.Replace(autofixSource, "// Keep this comment.", "//nolint:bubblelint // Intentionally excluded.", 1),
					},
					{
						name:       "configured-exclusion",
						source:     autofixSource,
						exclusions: "    rules:\n      - path: 'fix_test\\.go$'\n        linters: [bubblelint]\n        text: 'ST003:'\n",
					},
				} {
					t.Run(tc.name, func(t *testing.T) {
						dir, path := writeAutofixModule(t, tc.source)
						original := readAutofixFile(t, path)
						stdout, stderr, exit := driver.run(t, dir, true, false, tc.exclusions)
						checkAutofixExit(t, exit, 0, stdout, stderr)
						checkAutofixUnchanged(t, path, original)
					})
				}
			}
		})
	}
}

func writeAutofixModule(t *testing.T, source string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/autofix\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "fix_test.go")
	if err := os.WriteFile(path, formatAutofixSource(t, []byte(source)), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func expectedAutofixSource(source string) string {
	return strings.Replace(source, "bt.Parallel() // Keep this comment.", "// Keep this comment.", 1)
}

func readAutofixFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func formatAutofixSource(t *testing.T, source []byte) []byte {
	t.Helper()
	data, err := format.Source(source)
	if err != nil {
		t.Fatalf("invalid Go source: %v\n%s", err, source)
	}
	return data
}

func checkAutofixSource(t *testing.T, path, want string) {
	t.Helper()
	got := formatAutofixSource(t, readAutofixFile(t, path))
	wantBytes := formatAutofixSource(t, []byte(want))
	if !bytes.Equal(got, wantBytes) {
		t.Fatalf("unexpected fixed source\ngot:\n%s\nwant:\n%s", got, wantBytes)
	}
}

func checkAutofixUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	if got := readAutofixFile(t, path); !bytes.Equal(got, want) {
		t.Fatalf("unexpected source modification\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func checkAutofixExit(t *testing.T, got, want int, stdout, stderr string) {
	t.Helper()
	if got != want {
		t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", got, want, stdout, stderr)
	}
}
