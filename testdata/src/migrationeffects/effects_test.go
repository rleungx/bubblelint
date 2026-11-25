package migrationeffects

import (
	"testing"
	"time"

	"migrationeffects/relay"
	"migrationeffects/worker"
)

func TestImportedTimer(t *testing.T) {
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

func TestTransitiveImportedTimer(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Delay()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102: review this local timer-based worker"
		t.Fatal("worker did not finish")
	}
}

func TestConcreteImportedInterface(t *testing.T) {
	var sleeper worker.Sleeper = worker.Local{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sleeper.Wait()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102: review this local timer-based worker"
		t.Fatal("worker did not finish")
	}
}

func TestImportedNetwork(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Delay()
		relay.Fetch()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestConcreteImportedNetworkInterface(t *testing.T) {
	var sleeper worker.Sleeper = worker.Remote{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sleeper.Wait()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestImportedCallback(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(worker.Delay)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102: review this local timer-based worker"
		t.Fatal("worker did not finish")
	}
}
