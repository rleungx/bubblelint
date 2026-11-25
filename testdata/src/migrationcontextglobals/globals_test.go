package migrationcontextglobals

import (
	"migrationcontextglobals/worker"
	"testing"
	"time"
)

func TestImportedGlobalDuration(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.Delay() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: package-level state has not been isolated"
		t.Fatal("worker did not finish")
	}
}

func TestImportedGlobalBranch(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.Branch() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: package-level state has not been isolated"
		t.Fatal("worker did not finish")
	}
}
