package migrationexplain

import (
	"sync"
	"testing"
	"time"

	"migrationeffects/relay"
	"migrationeffects/worker"
)

func TestNetwork(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Delay()
		relay.Fetch()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O is reachable via .*relay.Fetch -> .*worker.Fetch -> net.Dial"
		t.Fatal("worker did not finish")
	}
}

func TestCallback(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(worker.UnknownCallback())
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: the callback target remains unknown for these arguments"
		t.Fatal("worker did not finish")
	}
}

func TestUnknownInterface(t *testing.T) {
	sleeper := worker.NewSleeper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sleeper.Wait()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: the interface call target is unknown"
		t.Fatal("worker did not finish")
	}
}

func TestClockOrdering(t *testing.T) {
	before := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Delay()
	}()
	select {
	case <-done:
		if !time.Now().After(before) {
			t.Fatal("clock did not advance")
		}
	case <-time.After(time.Second): // want "migration unproven: clock reads need an assertion-ordering review"
		t.Fatal("worker did not finish")
	}
}

func TestMutex(t *testing.T) {
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		mu.Lock()
		defer mu.Unlock()
		worker.Delay()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: mutex contention and progress have not been established"
		t.Fatal("worker did not finish")
	}
}

func TestNegativeAssertion(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Fetch()
	}()
	select {
	case <-done:
		t.Fatal("worker finished before the timeout")
	case <-time.After(time.Second):
	}
}
