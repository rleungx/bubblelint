package misuse

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitCallbacksOutside(t *testing.T) {
	t.Cleanup(
		synctest.Wait, // want "ST001:"
	)
	wait := synctest.Wait
	alias := wait
	t.Cleanup(
		alias, // want "ST001:"
	)
	(*testing.T).Cleanup(t,
		synctest.Wait, // want "ST001:"
	)
	cleanup := (*testing.T).Cleanup
	cleanup(t,
		alias, // want "ST001:"
	)
	time.AfterFunc(time.Nanosecond,
		synctest.Wait, // want "ST001:"
	)
	after := time.AfterFunc
	after(time.Nanosecond,
		alias, // want "ST001:"
	)
	var wg sync.WaitGroup
	wg.Go(
		synctest.Wait, // want "ST001:"
	)
	wg.Go(
		alias, // want "ST001:"
	)
	(*sync.WaitGroup).Go(&wg,
		synctest.Wait, // want "ST001:"
	)
	start := (*sync.WaitGroup).Go
	start(&wg,
		alias, // want "ST001:"
	)
	wg.Wait()
}

func TestWaitCallbacksOwnedOutside(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		t.Cleanup(
			synctest.Wait, // want "ST001:"
		)
		wait := synctest.Wait
		(*testing.T).Cleanup(t,
			wait, // want "ST001:"
		)
		cleanup := (*testing.T).Cleanup
		cleanup(t,
			wait, // want "ST001:"
		)
	})
}

func TestParallelCallbacks(t *testing.T) {
	synctest.Test(t,
		(*testing.T).Parallel, // want "ST003:"
	)
	parallel := (*testing.T).Parallel
	alias := parallel
	synctest.Test(t,
		alias, // want "ST003:"
	)
	synctest.Test(t, func(bt *testing.T) {
		bt.Cleanup(
			bt.Parallel, // want "ST003:"
		)
		parallel := bt.Parallel
		bt.Cleanup(
			parallel, // want "ST003:"
		)
		// Restriction follows the receiver even if execution is outside.
		t.Cleanup(
			parallel, // want "ST003:"
		)
		(*testing.T).Cleanup(bt,
			parallel, // want "ST003:"
		)
		time.AfterFunc(time.Nanosecond,
			bt.Parallel, // want "ST003:"
		)
		time.AfterFunc(time.Nanosecond,
			parallel, // want "ST003:"
		)
		var wg sync.WaitGroup
		wg.Go(
			bt.Parallel, // want "ST003:"
		)
		start := (*sync.WaitGroup).Go
		start(&wg,
			parallel, // want "ST003:"
		)
		wg.Wait()
	})
}
