package clean

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitCallbacksInside(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		wait := synctest.Wait
		alias := wait
		bt.Cleanup(synctest.Wait)
		bt.Cleanup(alias)
		(*testing.T).Cleanup(bt, synctest.Wait)
		cleanup := (*testing.T).Cleanup
		cleanup(bt, alias)
		time.AfterFunc(time.Nanosecond, synctest.Wait)
		after := time.AfterFunc
		after(time.Nanosecond, alias)
		var wg sync.WaitGroup
		wg.Go(synctest.Wait)
		wg.Go(alias)
		(*sync.WaitGroup).Go(&wg, synctest.Wait)
		start := (*sync.WaitGroup).Go
		start(&wg, alias)
		wg.Wait()
		t.Run("ordinary T", func(sub *testing.T) {
			sub.Cleanup(synctest.Wait)
			cleanup(sub, alias)
		})
	})
}

func TestOrdinaryParallelCallbacks(t *testing.T) {
	parallel := (*testing.T).Parallel
	t.Run("direct", (*testing.T).Parallel)
	t.Run("alias", parallel)
	run := (*testing.T).Run
	run(t, "method expression", parallel)
}

func TestOrdinaryParallelCallbacksInside(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		bt.Cleanup(t.Parallel)
		parallel := t.Parallel
		bt.Cleanup(parallel)
		t.Run("ordinary T", (*testing.T).Parallel)
	})
}

func TestUnknownAndShadowedCallbacks(t *testing.T) {
	t.Cleanup(fake{}.Wait)
	t.Cleanup(fake{}.Parallel)
	time.AfterFunc(time.Nanosecond, fake{}.Wait)
	var wg sync.WaitGroup
	wg.Go(fake{}.Wait)
	wg.Wait()
	context.AfterFunc(t.Context(), synctest.Wait)
	store(synctest.Wait)
	wait := synctest.Wait
	wait = func() {}
	t.Cleanup(wait)
	unknownCleanupOwner(t)
}

func unknownCleanupOwner(t *testing.T) {
	t.Cleanup(synctest.Wait)
	t = nil
}
