package migrationboundary

import (
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func waitForTimer() { time.Sleep(time.Millisecond) }

func outerCleanup(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	// This panics if a migration accidentally moves the defer into the bubble.
	synctest.Test(t, func(t *testing.T) {})
}

func TestOuterCleanup(t *testing.T) {
	defer outerCleanup(t)
	done := make(chan struct{})
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:.*keep the leading defers outside"
		t.Fatal("worker did not finish")
	}
}

func TestOuterClosure(t *testing.T) {
	defer func() { outerCleanup(t) }()
	done := make(chan struct{})
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:.*keep the leading defers outside"
		t.Fatal("worker did not finish")
	}
}

func TestLateCleanup(t *testing.T) {
	done := make(chan struct{})
	defer outerCleanup(t)
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestInnerCleanup(t *testing.T) {
	defer outerCleanup(t)
	done := make(chan struct{})
	defer outerCleanup(t)
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func networkArgument() *testing.T {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	listener.Close()
	return nil
}

func TestEvaluatedArgument(t *testing.T) {
	defer outerCleanup(networkArgument())
	done := make(chan struct{})
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type cleanupReceiver struct{ t *testing.T }

func (r *cleanupReceiver) finish() { outerCleanup(r.t) }

func newCleanupReceiver() *cleanupReceiver { return &cleanupReceiver{t: networkArgument()} }

func TestEvaluatedReceiver(t *testing.T) {
	defer newCleanupReceiver().finish()
	done := make(chan struct{})
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestWorkerNetwork(t *testing.T) {
	defer outerCleanup(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForTimer()
		listener, _ := net.Listen("tcp", "127.0.0.1:0")
		listener.Close()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestOuterTimerOnly(t *testing.T) {
	defer outerCleanup(t)
	defer waitForTimer()
	done := make(chan struct{})
	go func() { defer close(done); noTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func noTimer() {}

func TestStrictProtocol(t *testing.T) {
	defer outerCleanup(t)
	done := make(chan struct{})
	go func() { defer close(done); time.Sleep(time.Millisecond) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestBorrowedChannel(t *testing.T) {
	shared := make(chan struct{})
	t.Run("child", func(t *testing.T) {
		defer outerCleanup(t)
		done := make(chan struct{})
		go func() { defer close(done); <-shared; waitForTimer() }()
		select {
		case <-done:
		case <-time.After(time.Second): // want "migration unproven: conditional path: the channel's bubble ownership"
			t.Fatal("worker did not finish")
		}
	})
}

func TestNestedWorker(t *testing.T) {
	defer outerCleanup(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		synctest.Test(t, func(t *testing.T) { waitForTimer() })
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: this path already starts a synctest bubble"
		t.Fatal("worker did not finish")
	}
}

func TestExistingBubbleBeforeWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {})
	done := make(chan struct{})
	go func() { defer close(done); waitForTimer() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: this path already starts a synctest bubble"
		t.Fatal("worker did not finish")
	}
}
