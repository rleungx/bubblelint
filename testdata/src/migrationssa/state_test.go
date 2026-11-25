package migrationssa

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"migrationssa/opaque"
)

type worker struct {
	ctx     context.Context
	cancel  context.CancelFunc
	target  atomic.Value
	enabled atomic.Bool
}

func newWorker(parent context.Context) *worker {
	ctx, cancel := context.WithCancel(parent)
	w := &worker{ctx: ctx, cancel: cancel}
	return w
}

func fetch() { _, _ = net.Dial("tcp", "127.0.0.1:1") }

func (w *worker) run() {
	target := w.target.Load()
	if target == nil {
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(time.Millisecond):
		}
		return
	}
	fetch()
}

func (w *worker) runEnabled() {
	if w.enabled.Load() {
		fetch()
	} else {
		time.Sleep(time.Millisecond)
	}
}

func TestZeroAtomic(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestStoredAtomic(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	w.target.Store("remote")
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestAliasMutation(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	alias := w
	alias.enabled.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestMutationAfterLaunch(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	w.enabled.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	w.enabled.Store(false)
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type backend interface{ Run() }
type local struct{}

func (*local) Run() { time.Sleep(time.Millisecond) }

type remote struct{}

func (*remote) Run() { fetch() }

type service struct{ backend backend }

func newService(b backend) *service { s := &service{}; s.backend = b; return s }
func (s *service) run()             { s.backend.Run() }

func TestConcreteInterface(t *testing.T) {
	s := newService(&local{})
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestRemoteInterface(t *testing.T) {
	s := newService(&remote{})
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestTypedNilAtomic(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	w.target.Store((*worker)(nil))
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestEscapedAtomic(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	opaque.Touch(&w.enabled)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestConditionalStore(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	if testing.Short() {
		w.enabled.Store(true)
	}
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func disable(w *worker) { w.enabled.Store(false) }

func TestLaterHelperStore(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	w.enabled.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	disable(w)
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestDynamicIndexMutation(t *testing.T) {
	var workers [2]worker
	index := 0
	if testing.Short() {
		index = 1
	}
	workers[index].enabled.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); workers[0].runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestSliceAliasMutation(t *testing.T) {
	var workers [2]worker
	alias := workers[1:]
	alias[0].enabled.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); workers[1].runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

type plainWorker struct{ enabled bool }

func (w *plainWorker) run() {
	if w.enabled {
		fetch()
	} else {
		time.Sleep(time.Millisecond)
	}
}

func TestSelectPayloadEscape(t *testing.T) {
	w := &plainWorker{}
	updates := make(chan *bool, 1)
	select {
	case updates <- &w.enabled:
	default:
	}
	opaque.TouchThrough(updates)
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}
