package migrationreview

import (
	"net"
	"testing"
	"time"
)

func waitForTimer() { <-time.After(time.Millisecond) }

func TestLocalWorkerHelper(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForTimer()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

type timerWorker struct{}

func (*timerWorker) run() { time.Sleep(time.Millisecond) }

func TestMethodHelper(t *testing.T) {
	done := make(chan struct{})
	worker := &timerWorker{}
	go func() {
		defer close(done)
		worker.run()
	}()
	select {
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	case <-done:
	}
}

func waitThroughHelper() { waitForTimer() }

func TestTransitiveTimerHelper(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitThroughHelper()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

type localGate struct {
	entered chan struct{}
	release chan struct{}
}

func (g *localGate) wait() {
	close(g.entered)
	<-g.release
}

func TestReadinessSubtest(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
		done := make(chan struct{})
		go func() {
			defer close(done)
			gate.wait()
		}()
		select {
		case <-gate.entered:
		case <-time.After(time.Second): // want "ST102:"
			close(gate.release)
			t.Fatal("worker did not reach the gate")
		}
		close(gate.release)
		<-done
	})
}

func waitForNetwork() {
	time.Sleep(time.Millisecond)
	conn, _ := net.Dial("tcp", "127.0.0.1:1")
	if conn != nil {
		conn.Close()
	}
}

func TestNetworkHelper(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForNetwork()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func unusedTimer() { _ = func() { time.Sleep(time.Millisecond) } }

func TestUnusedTimer(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		unusedTimer()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func unreachableTimer() {
	if false {
		time.Sleep(time.Millisecond)
	}
}

func TestUnreachableTimer(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		unreachableTimer()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func startUnjoinedTimer() {
	go func() { time.Sleep(time.Millisecond) }()
}

func TestUnjoinedTimer(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		startUnjoinedTimer()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestReadinessWithoutJoin(t *testing.T) {
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		signalReady(ready)
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("worker did not signal readiness")
	}
}

func signalReady(ch chan struct{}) { close(ch) }

func TestVariableTimeout(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForTimer()
	}()
	timeout := time.Second
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("worker did not finish")
	}
}
