package clean

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestCorrect(t *testing.T) {
	t.Parallel()
	_, _ = t.Deadline()
	t.Run("bubble", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			helper()
			go helper()
			t.Cleanup(helper)
			func() { synctest.Wait() }()
			const timeout = time.Second
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			time.Sleep(timeout - time.Nanosecond)
			synctest.Wait()
			_ = ctx.Err()
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			done := make(chan struct{})
			go func() { close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		})
	})
}

func helper() { synctest.Wait() }

// Uncalled helpers have no known execution context.
func uncalled(t *testing.T) {
	synctest.Wait()
	t.Parallel()
}

func TestUnknownCallback(t *testing.T) {
	store(func() { synctest.Wait() })
	_ = func() { synctest.Wait() }
}

func store(func()) {}

func TestSleepAlone(t *testing.T) {
	time.Sleep(time.Nanosecond)
	select {
	case <-time.After(time.Nanosecond):
	}
}

func TestWorkerSleep(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(time.Nanosecond)
	}()
	wg.Wait()
	time.Sleep(0)
	time.Sleep(-1)
}

func TestOuterT(t *testing.T) {
	outer := t
	synctest.Test(t, func(t *testing.T) {
		_, _ = outer.Deadline()
		t.Cleanup(func() { synctest.Wait() })
	})
}

func TestOuterSubtest(t *testing.T) {
	outer := t
	synctest.Test(t, func(t *testing.T) {
		outer.Run("same bubble", func(t *testing.T) {
			synctest.Wait()
			_, _ = t.Deadline()
		})
	})
}

func TestRecursive(t *testing.T) { recursive(0) }
func recursive(n int) {
	if n > 0 {
		recursive(n - 1)
	}
}

type fake struct{}

func (fake) Wait()                             {}
func (fake) Test(*testing.T, func(*testing.T)) {}
func (fake) Sleep(time.Duration)               {}
func (fake) Parallel()                         {}

func TestShadowed(t *testing.T) {
	synctest, time := fake{}, fake{}
	synctest.Wait()
	synctest.Test(t, func(t *testing.T) { t.Parallel() })
	go func() {}()
	time.Sleep(1)
}

func TestMutableFunction(t *testing.T) {
	wait := synctest.Wait
	wait = func() {}
	wait()
}

func TestReassignedT(t *testing.T) {
	outer := t
	synctest.Test(t, func(t *testing.T) {
		t = outer
		_, _ = t.Deadline()
	})
}

func TestWaitGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Go(helper)
		wg.Wait()
	})
}

func TestContextCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	context.AfterFunc(ctx, func() { synctest.Wait() })
}

func TestNamedBubble(t *testing.T) { synctest.Test(t, bubble) }
func bubble(t *testing.T)          { helper() }

func TestGeneric(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { generic[int]() })
}
func generic[T any]() { synctest.Wait() }

// Lowercase names are not testing entry points.
func Testhelper(t *testing.T) { synctest.Wait() }
