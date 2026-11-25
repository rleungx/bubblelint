package migrationcontext

import (
	"testing"
	"time"

	"migrationcontext/worker"
)

func TestBoolFalse(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.MaybeFetch(false) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestBoolTrue(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.MaybeFetch(true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestNilBackend(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.WithBackend(nil) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestPureCallback(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(func() { worker.Delay() }) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestNetworkCallback(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(worker.Fetch) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestReceiver(t *testing.T) {
	task := &worker.Task{Remote: false}
	done := make(chan struct{})
	go func() { defer close(done); task.Run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestConstructor(t *testing.T) {
	task := worker.NewTask(false)
	done := make(chan struct{})
	go func() { defer close(done); task.Run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestMutatedReceiver(t *testing.T) {
	task := &worker.Task{Remote: false}
	task.Remote = true
	done := make(chan struct{})
	go func() { defer close(done); task.Run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestMutatedConstructor(t *testing.T) {
	task := worker.NewTask(false)
	task.Remote = true
	done := make(chan struct{})
	go func() { defer close(done); task.Run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestBothBranches(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.BothBranches(false) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestRecursiveBaseCase(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.Recursive(false) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestRecursiveUnknownProgress(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); worker.Recursive(true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestRegisteredNetworkCallback(t *testing.T) {
	worker.RegisterCleanup(t, worker.Fetch)
	done := make(chan struct{})
	go func() { defer close(done); worker.Delay() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func nestedReturn(t *testing.T, a, b bool) {
	done := make(chan struct{})
	go func() { defer close(done); worker.NestedReturn(a, b) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestNestedFirst(t *testing.T)  { nestedReturn(t, false, false) }
func TestNestedSecond(t *testing.T) { nestedReturn(t, true, true) }
