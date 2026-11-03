package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

const timeoutFixSource = `package autofix

import (
	"testing"
	"time"
)

func TestClose(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		time.Sleep(2 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
		t.Log("worker finished")
	case <-time.After(time.Second): // Keep the original timeout.
		t.Fatal("worker timed out")
	}
}

func TestSend(t *testing.T) {
	result := make(chan int)
	go func() {
		time.Sleep(time.Millisecond)
		result <- 42
	}()
	select {
	case got := <-result:
		if got != 42 { t.Fatalf("got %d, want 42", got) }
	case <-time.After(time.Second):
		t.Fatal("worker timed out")
	}
}

func TestCommaOK(t *testing.T) {
	result := make(chan int)
	go func() {
		time.Sleep(time.Millisecond)
		close(result)
	}()
	select {
	case got, ok := <-result:
		if ok || got != 0 { t.Fatalf("closed channel returned (%d, %t)", got, ok) }
	case <-time.After(time.Second):
		t.Fatal("worker timed out")
	}
}

func TestSleepAndTimeout(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		close(done)
	}()
	time.Sleep(time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker timed out")
	}
}

func TestFinalWorker(t *testing.T) {
	first := make(chan struct{})
	last := make(chan bool)
	go func() { close(first) }()
	go func() {
		time.Sleep(time.Millisecond)
		last <- true
	}()
	<-first
	select {
	case got := <-last:
		if !got { t.Fatal("worker did not finish") }
	case <-time.After(time.Second):
		t.Fatal("worker timed out")
	}
}
`

const unsupportedTimeoutSource = `
var timeoutDelay = time.Second
var timeoutGlobal = make(chan struct{})

func finishTimeout(done chan struct{}) { close(done) }

func TestTooShort(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(2 * time.Millisecond); close(done) }()
	select { case <-done: case <-time.After(time.Millisecond): t.Fatal("timeout") }
}

func TestEqualDelay(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(time.Millisecond); close(done) }()
	select { case <-done: case <-time.After(time.Millisecond): t.Fatal("timeout") }
}

func TestCumulativeDelay(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(time.Millisecond); time.Sleep(2 * time.Millisecond); close(done) }()
	select { case <-done: case <-time.After(2 * time.Millisecond): t.Fatal("timeout") }
}

func TestUnknownDelay(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select { case <-done: case <-time.After(timeoutDelay): t.Fatal("timeout") }
}

func TestGlobalChannel(t *testing.T) {
	go func() { close(timeoutGlobal) }()
	select { case <-timeoutGlobal: case <-time.After(time.Second): t.Fatal("timeout") }
}

func TestSharedChannel(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	go func() { close(done) }()
	select { case <-done: case <-time.After(time.Second): t.Fatal("timeout") }
}

func TestExtraCase(t *testing.T) {
	done := make(chan struct{})
	extra := make(chan struct{})
	go func() { close(done) }()
	select { case <-done: case <-extra: case <-time.After(time.Second): t.Fatal("timeout") }
}

func TestDefaultCase(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select { case <-done: case <-time.After(time.Second): t.Fatal("timeout"); default: }
}

func TestSelectSend(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select { case done <- struct{}{}: case <-time.After(time.Second): t.Fatal("timeout") }
}

func TestTwoUnjoinedWorkers(t *testing.T) {
	done := make(chan struct{})
	other := make(chan struct{})
	go func() { close(done) }()
	go func() { close(other) }()
	select { case <-done: case <-time.After(time.Second): t.Fatal("timeout") }
	<-other
}

func TestAfterSignal(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done); time.Sleep(time.Millisecond) }()
	select { case <-done: case <-time.After(time.Second): t.Fatal("timeout") }
}

func TestHelper(t *testing.T) {
	done := make(chan struct{})
	go finishTimeout(done)
	select { case <-done: case <-time.After(time.Second): t.Fatal("timeout") }
}

func TestZeroTimeout(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select { case <-done: case <-time.After(0): t.Fatal("timeout") }
}
`

func TestTimeoutMigrationFix(t *testing.T) {
	for _, driver := range []autofixDriver{
		{name: "CLI", binary: *cliBinary},
		{name: "golangci-lint", binary: *golangciBinary, golangci: true},
	} {
		t.Run(driver.name, func(t *testing.T) {
			if driver.binary == "" {
				t.Skip("run make integration to test timeout migration fixes")
			}
			var err error
			driver.binary, err = filepath.Abs(driver.binary)
			if err != nil {
				t.Fatal(err)
			}
			diagnosticExit, remainingFixExit := 3, 0
			if driver.golangci {
				diagnosticExit, remainingFixExit = 1, 1
			}
			migrated := []string{"TestClose", "TestSend", "TestCommaOK", "TestSleepAndTimeout", "TestFinalWorker"}

			for _, alias := range []string{"", "st"} {
				name, source := "defaults-add-import", timeoutFixSource
				if alias != "" {
					name = "defaults-reuse-import-alias"
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
					for _, code := range []string{"ST100:", "ST101:"} {
						if !strings.Contains(stdout+stderr, code) {
							t.Errorf("missing suggestion %s\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
						}
					}
					checkAutofixUnchanged(t, path, original)
					stdout, stderr, exit = driver.runMigration(t, dir, true, true, "")
					checkAutofixExit(t, exit, 0, stdout, stderr)
					checkMigratedFunctions(t, path, original, migrated, alias)
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

			t.Run("excluded-migration-rules", func(t *testing.T) {
				dir, path := writeAutofixModule(t, timeoutFixSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.runMigration(t, dir, true, false, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkAutofixUnchanged(t, path, original)
			})

			t.Run("preserve-unsupported-tests", func(t *testing.T) {
				dir, path := writeAutofixModule(t, timeoutFixSource+unsupportedTimeoutSource)
				original := readAutofixFile(t, path)
				stdout, stderr, exit := driver.runMigration(t, dir, true, true, "")
				checkAutofixExit(t, exit, remainingFixExit, stdout, stderr)
				checkMigratedFunctions(t, path, original, migrated, "")
				fixed := readAutofixFile(t, path)
				stdout, stderr, exit = driver.runMigration(t, dir, false, true, "")
				checkAutofixExit(t, exit, diagnosticExit, stdout, stderr)
				if !strings.Contains(stdout+stderr, "ST101:") {
					t.Fatalf("missing unsupported timeout suggestions\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
				}
				stdout, stderr, exit = driver.runMigration(t, dir, true, true, "")
				checkAutofixExit(t, exit, remainingFixExit, stdout, stderr)
				checkAutofixUnchanged(t, path, fixed)
			})

			t.Run("preserve-assertion-failure", func(t *testing.T) {
				source := strings.Replace(timeoutFixSource, `t.Log("worker finished")`, `t.Fatal("intentional receive failure")`, 1)
				dir, path := writeAutofixModule(t, source)
				original := readAutofixFile(t, path)
				checkFailure := func() {
					t.Helper()
					stdout, stderr, exit := runCommand(t, dir, "go", "test", "-race", "-count=1", "-timeout=30s", "-run=^TestClose$", "./...")
					checkAutofixExit(t, exit, 1, stdout, stderr)
					output := stdout + stderr
					if !strings.Contains(output, "intentional receive failure") || strings.Contains(output, "deadlock") || strings.Contains(output, "test timed out") {
						t.Fatalf("expected the original assertion failure without deadlock\n%s", output)
					}
				}
				checkFailure()
				stdout, stderr, exit := driver.runMigration(t, dir, true, true, "")
				checkAutofixExit(t, exit, 0, stdout, stderr)
				checkMigratedFunctions(t, path, original, migrated, "")
				checkFailure()
			})
		})
	}
}
