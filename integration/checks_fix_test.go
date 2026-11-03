package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

const selectiveFixSource = `package autofix

import (
	"testing"
	"time"
)

func TestSleepOnly(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(time.Millisecond); close(done) }()
	time.Sleep(time.Millisecond)
	<-done
}

func TestTimeoutOnly(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker timed out")
	}
}

func TestBoth(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(time.Millisecond); close(done) }()
	time.Sleep(time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker timed out")
	}
}
`

func TestChecksControlMigrationFixes(t *testing.T) {
	for _, driver := range []autofixDriver{
		{name: "CLI", binary: *cliBinary},
		{name: "golangci-lint", binary: *golangciBinary, golangci: true},
	} {
		t.Run(driver.name, func(t *testing.T) {
			if driver.binary == "" {
				t.Skip("run make integration to test rule selection and fixes")
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
			for _, tc := range []struct {
				check, excluded string
				migrated        []string
			}{
				{"ST100", "ST101", []string{"TestSleepOnly", "TestBoth"}},
				{"ST101", "ST100", []string{"TestTimeoutOnly", "TestBoth"}},
			} {
				t.Run(tc.check, func(t *testing.T) {
					dir, path := writeAutofixModule(t, selectiveFixSource)
					original := readAutofixFile(t, path)
					checks := []string{tc.check}
					stdout, stderr, exit := driver.runChecks(t, dir, false, checks, "")
					checkAutofixExit(t, exit, diagnosticExit, stdout, stderr)
					if output := stdout + stderr; !strings.Contains(output, tc.check+":") || strings.Contains(output, tc.excluded+":") {
						t.Fatalf("expected only %s diagnostics\n%s", tc.check, output)
					}
					checkAutofixUnchanged(t, path, original)

					stdout, stderr, exit = driver.runChecks(t, dir, true, checks, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					// The mixed test migrates as a whole when either rule is
					// selected. The disabled rule's separate test stays unchanged.
					checkMigratedFunctions(t, path, original, tc.migrated, "")
					fixed := readAutofixFile(t, path)
					stdout, stderr, exit = runCommand(t, dir, "go", "test", "-race", "-count=1", "-timeout=30s", "./...")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					stdout, stderr, exit = driver.runChecks(t, dir, true, checks, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					checkAutofixUnchanged(t, path, fixed)
					stdout, stderr, exit = driver.runChecks(t, dir, false, checks, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
				})
			}

			t.Run("empty-selection", func(t *testing.T) {
				dir, path := writeAutofixModule(t, selectiveFixSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.runChecks(t, dir, true, []string{}, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkAutofixUnchanged(t, path, original)
			})
		})
	}
}
