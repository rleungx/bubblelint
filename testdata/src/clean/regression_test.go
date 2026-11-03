package clean

import (
	"sync"
	"testing"
	"testing/synctest"
)

func TestFunctionParameterMutation(t *testing.T) {
	mutatedCallback(func() {})
	func(callback func()) {
		callback()
		callback = synctest.Wait
	}(func() {})
}

func mutatedCallback(callback func()) {
	callback()
	callback = synctest.Wait
}

func TestRangeFunctionMutation(t *testing.T) {
	wait := synctest.Wait
	for _, wait = range []func(){func() {}} {
		wait()
	}
}

func TestParenthesizedFunctionMutation(t *testing.T) {
	wait := synctest.Wait
	(wait) = func() {}
	wait()
}

func TestRangeTMutation(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		for _, bt = range []*testing.T{t} {
			_, _ = bt.Deadline()
		}
	})
}

func TestParenthesizedTMutation(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		(bt) = t
		_, _ = bt.Deadline()
	})
}

func TestSubtestCleanupInBubble(t *testing.T) {
	outer := t
	synctest.Test(t, func(bt *testing.T) {
		outer.Run("same bubble", func(sub *testing.T) {
			sub.Cleanup(synctest.Wait)
			sub.Cleanup(func() { synctest.Wait() })
			alias := sub
			alias.Cleanup(func() { synctest.Wait() })
			registerBubbleCleanup(alias)
			func() {
				func() { registerBubbleCleanup(sub) }()
			}()
			_, _ = sub.Deadline()
		})
	})
}

func registerBubbleCleanup(t *testing.T) {
	t.Cleanup(func() { synctest.Wait() })
}

func TestUnknownCleanupOwner(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		registerUnknownCleanup(bt)
	})
}

func registerUnknownCleanup(t *testing.T) {
	t.Cleanup(func() { synctest.Wait() })
	t = nil
}

func TestMethodExpressionsInBubble(t *testing.T) {
	outer := t
	synctest.Test(t, func(bt *testing.T) {
		_, _ = (*testing.T).Deadline(outer)
		deadline := (*testing.T).Deadline
		_, _ = deadline(outer)
		(*testing.T).Cleanup(bt, func() { synctest.Wait() })
		cleanup := (*testing.T).Cleanup
		cleanup(bt, func() { synctest.Wait() })
		(*testing.T).Run(outer, "direct", func(sub *testing.T) {
			_, _ = (*testing.T).Deadline(sub)
			(*testing.T).Cleanup(sub, func() { synctest.Wait() })
		})
		run := (*testing.T).Run
		run(outer, "alias", func(sub *testing.T) {
			_, _ = deadline(sub)
			cleanup(sub, func() { synctest.Wait() })
		})
		var wg sync.WaitGroup
		(*sync.WaitGroup).Go(&wg, helper)
		start := (*sync.WaitGroup).Go
		start(&wg, helper)
		wg.Wait()
	})
}

func TestParallelMethodExpression(t *testing.T) {
	(*testing.T).Parallel(t)
}

func TestParallelMethodExpressionAlias(t *testing.T) {
	parallel := (*testing.T).Parallel
	parallel(t)
}
