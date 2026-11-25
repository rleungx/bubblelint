package migrationcontextmutation

import (
	"net"
	"testing"
	"time"
)

type task struct{ remote bool }

func mutate(t *task) { t.remote = true }

func fetch() { _, _ = net.Dial("tcp", "127.0.0.1:1") }

func (t *task) run() {
	if t.remote {
		fetch()
	} else {
		time.Sleep(time.Millisecond)
	}
}

func mutateAndRun(t *task) { mutate(t); t.run() }

func TestMutatingCallee(t *testing.T) {
	worker := &task{remote: false}
	done := make(chan struct{})
	go func() { defer close(done); mutateAndRun(worker) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}
