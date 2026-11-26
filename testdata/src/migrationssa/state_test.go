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

func setupLoop() {
	for i := 0; i < 2; i++ {
		time.Sleep(time.Nanosecond)
	}
}

func setupRecursive(n int) {
	if n > 0 {
		setupRecursive(n - 1)
	}
}

func TestIndependentSetup(t *testing.T) {
	setupLoop()
	setupRecursive(2)
	w := newWorker(t.Context())
	defer w.cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: loop termination"
		t.Fatal("worker did not finish")
	}
}

func recursiveMutation(w *worker, n int) {
	if n > 0 {
		recursiveMutation(w, n-1)
	} else {
		w.enabled.Store(true)
	}
}

func TestRecursiveMutation(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	recursiveMutation(w, 2)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestCallbackMutation(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	time.AfterFunc(time.Nanosecond, func() { w.enabled.Store(true) })
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestCallbackNetwork(t *testing.T) {
	t.Cleanup(fetch)
	w := newWorker(t.Context())
	defer w.cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestFreshSubtest(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		w := newWorker(t.Context())
		defer w.cancel()
		done := make(chan struct{})
		go func() { defer close(done); w.run() }()
		select {
		case <-done:
		case <-time.After(time.Second): // want "ST102:"
			t.Fatal("worker did not finish")
		}
	})
}

func TestBorrowedSubtest(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	t.Run("borrowed", func(t *testing.T) {
		done := make(chan struct{})
		go func() { defer close(done); w.run() }()
		select {
		case <-done:
		case <-time.After(time.Second): // want "migration unproven:"
			t.Fatal("worker did not finish")
		}
	})
}

func TestParentContextSubtest(t *testing.T) {
	t.Run("parent-context", func(st *testing.T) {
		w := newWorker(t.Context())
		defer w.cancel()
		done := make(chan struct{})
		go func() { defer close(done); w.run() }()
		select {
		case <-done:
		case <-time.After(time.Second): // want "migration unproven:"
			st.Fatal("worker did not finish")
		}
	})
}

type namedFlag bool
type namedWorker struct{ enabled namedFlag }

func (w *namedWorker) run() {
	if w.enabled {
		fetch()
	} else {
		time.Sleep(time.Millisecond)
	}
}

func TestConvertedAliasMutation(t *testing.T) {
	w := &namedWorker{}
	alias := (*bool)(&w.enabled)
	*alias = true
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestAggregateMutation(t *testing.T) {
	w := &plainWorker{}
	*w = plainWorker{enabled: true}
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestClosureWithoutSummary(t *testing.T) {
	callbacks := &struct{ run func() }{run: func() {
		for i := 0; i < 2; i++ {
			time.Sleep(time.Millisecond)
		}
	}}
	done := make(chan struct{})
	go func() { defer close(done); callbacks.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: the function has no usable effect summary"
		t.Fatal("worker did not finish")
	}
}

func TestBorrowedChannelSubtest(t *testing.T) {
	fetch()
	shared := make(chan struct{})
	t.Run("borrowed-channel", func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-shared
			time.Sleep(time.Millisecond)
		}()
		time.Sleep(time.Millisecond) // want "migration unproven: conditional path: the channel's bubble ownership"
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("worker did not finish")
		}
	})
}

func TestImportedConcreteInterface(t *testing.T) {
	s := newService(&opaque.Local{})
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestImportedRemoteInterface(t *testing.T) {
	s := newService(&opaque.Remote{})
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type promotedLocal struct{ *local }
type promotedImported struct{ *opaque.Local }

func TestPromotedInterface(t *testing.T) {
	s := newService(&promotedLocal{local: &local{}})
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestPromotedImportedInterface(t *testing.T) {
	s := newService(&promotedImported{Local: &opaque.Local{}})
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestLoadedAliasEscape(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	holder := &struct{ worker *worker }{worker: w}
	holder.worker = w
	opaque.Touch(&holder.worker.enabled)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type aliasNode struct {
	next *aliasNode
	flag *atomic.Bool
}

func TestCyclicAliasEscape(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	node := &aliasNode{flag: &w.enabled}
	node.next = node
	opaque.Touch(node.next.next.flag)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestAggregateAliasEscape(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	holder := struct{ worker *worker }{worker: w}
	copy := holder
	opaque.Touch(&copy.worker.enabled)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func storeMaybe(local, external *atomic.Bool) {
	selected := local
	select {
	case <-time.After(time.Millisecond):
		selected = external
	default:
	}
	selected.Store(true)
}

func TestPartiallyKnownAlias(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	storeMaybe(&w.enabled, opaque.NewFlag())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if w.enabled.Load() {
			time.Sleep(time.Millisecond)
		} else {
			fetch()
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestUnstableDispatch(t *testing.T) {
	s := newService(&local{})
	opaque.Forget(&s.backend)
	w := newWorker(t.Context())
	defer w.cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.run(); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: the interface call target is unknown"
		t.Fatal("worker did not finish")
	}
}

type counterWorker struct{ counter atomic.Int64 }

func (w *counterWorker) run() {
	w.counter.Add(1)
	w.counter.And(7)
	w.counter.Or(1)
	w.counter.CompareAndSwap(1, 2)
	time.Sleep(time.Millisecond)
}

func TestAtomicUpdates(t *testing.T) {
	w := &counterWorker{}
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func rawAtomicWorker(flag *int64) {
	if atomic.LoadInt64(flag) != 0 {
		fetch()
	} else {
		time.Sleep(time.Millisecond)
	}
}

func TestRawAtomicInitialStore(t *testing.T) {
	flag := int64(1)
	done := make(chan struct{})
	go func() { defer close(done); rawAtomicWorker(&flag) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestRawAtomicStore(t *testing.T) {
	var flag int64
	atomic.StoreInt64(&flag, 1)
	done := make(chan struct{})
	go func() { defer close(done); rawAtomicWorker(&flag) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestAtomicUpdateBranch(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	w.enabled.CompareAndSwap(false, true)
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestAtomicPointerEscape(t *testing.T) {
	w := newWorker(t.Context())
	defer w.cancel()
	var pointer atomic.Pointer[worker]
	pointer.Swap(w)
	opaque.Apply(&pointer, func(actual *worker) { actual.enabled.Store(true) })
	done := make(chan struct{})
	go func() { defer close(done); w.runEnabled() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}
