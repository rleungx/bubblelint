package misuse

import (
	"sync"
	"testing"
	"testing/synctest"
)

func TestMethodExpressionsOnBubbleT(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		(*testing.T).Parallel(bt)                               // want "ST003:"
		_, _ = (*testing.T).Deadline(bt)                        // want "ST003:"
		(*testing.T).Run(bt, "direct", func(sub *testing.T) {}) // want "ST003:"
		parallel := (*testing.T).Parallel
		parallel(bt) // want "ST003:"
		deadline := (*testing.T).Deadline
		_, _ = deadline(bt) // want "ST003:"
		run := (*testing.T).Run
		run(bt, "alias", func(sub *testing.T) {}) // want "ST003:"
	})
}

func TestMethodExpressionCallbacksOutside(t *testing.T) {
	(*testing.T).Run(t, "direct", func(sub *testing.T) {
		synctest.Wait() // want "ST001:"
	})
	run := (*testing.T).Run
	run(t, "alias", func(sub *testing.T) {
		synctest.Wait() // want "ST001:"
	})
	(*testing.T).Cleanup(t, func() {
		synctest.Wait() // want "ST001:"
	})
	cleanup := (*testing.T).Cleanup
	cleanup(t, func() {
		synctest.Wait() // want "ST001:"
	})
	var wg sync.WaitGroup
	(*sync.WaitGroup).Go(&wg, func() {
		synctest.Wait() // want "ST001:"
	})
	start := (*sync.WaitGroup).Go
	start(&wg, func() {
		synctest.Wait() // want "ST001:"
	})
	wg.Wait()
}

func TestOuterCleanupForwarded(t *testing.T) {
	outer := t
	synctest.Test(t, func(bt *testing.T) {
		alias := outer
		registerOuterCleanup(alias)
		func() {
			func() {
				outer.Cleanup(func() {
					synctest.Wait() // want "ST001:"
				})
			}()
		}()
		(*testing.T).Cleanup(outer, func() {
			synctest.Wait() // want "ST001:"
		})
		cleanup := (*testing.T).Cleanup
		cleanup(alias, func() {
			synctest.Wait() // want "ST001:"
		})
	})
}

func registerOuterCleanup(t *testing.T) {
	t.Cleanup(func() {
		synctest.Wait() // want "ST001:"
	})
}
