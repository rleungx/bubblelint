package migrationconditional

import (
	"runtime"
	"testing"
	"time"

	"migrationconditional/worker"
)

func TestClearFinalizer(t *testing.T) {
	value := new(struct{ n int })
	runtime.SetFinalizer(value, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Delay()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102: review this local timer-based worker"
		t.Fatal("worker did not finish")
	}
}

func TestConditionalNetwork(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Delay()
		worker.MaybeFetch(false)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102: review this local timer-based worker"
		t.Fatal("worker did not finish")
	}
}

func TestConditionalOperand(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Delay()
		worker.Probe(false)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102: review this local timer-based worker"
		t.Fatal("worker did not finish")
	}
}

func TestCallAfterBranch(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.AlwaysFetch(false)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O is reachable"
		t.Fatal("worker did not finish")
	}
}

func TestRequiredWitness(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.MaybeFetch(false)
		worker.AlwaysFetch(false)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O is reachable via .*worker.AlwaysFetch -> .*worker.Fetch -> net.Dial"
		t.Fatal("worker did not finish")
	}
}
