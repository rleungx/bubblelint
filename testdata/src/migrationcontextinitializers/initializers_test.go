package migrationcontextinitializers

import (
	"net"
	"testing"
	"time"
)

func fetch() { _, _ = net.Dial("tcp", "127.0.0.1:1") }

func fetchFlag() bool { fetch(); return false }

func consume([]bool) {}

func sliceWorker() {
	consume([]bool{fetchFlag()})
	time.Sleep(time.Millisecond)
}

func TestSliceInitializer(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); sliceWorker() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type state struct{ remote bool }

func mutate(s *state) bool { s.remote = true; return false }

func inspect(s *state) bool { return s.remote }

type options struct {
	remote bool
	other  bool
}

func run(o options) {
	if o.remote {
		fetch()
	} else {
		time.Sleep(time.Millisecond)
	}
}

func orderedWorker(s *state) {
	run(options{other: mutate(s), remote: inspect(s)})
}

func TestStructInitializerOrder(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); orderedWorker(&state{}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type envelope struct{ worker *state }

func mutateNested(e *envelope) bool { e.worker.remote = true; return false }

func runPair(s *state, _ bool) { run(options{remote: s.remote}) }

func nestedWorker(e *envelope) { runPair(e.worker, mutateNested(e)) }

func TestNestedMutation(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); nestedWorker(&envelope{worker: &state{}}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}
