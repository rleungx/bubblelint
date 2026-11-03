package clean

import (
	"testing"
	"testing/synctest"
)

type cleanMethodHelper struct{}

func (cleanMethodHelper) wait() { synctest.Wait() }
func (cleanMethodHelper) ordinary(t *testing.T) {
	t.Parallel()
}
func (cleanMethodHelper) reassigned(t, other *testing.T) {
	t = other
	t.Parallel()
}

// A method whose name starts with Test is not a test entry point.
func (cleanMethodHelper) TestNotARoot(t *testing.T) { synctest.Wait() }

func TestCleanMethodHelpers(t *testing.T) {
	h := cleanMethodHelper{}
	synctest.Test(t, func(bt *testing.T) {
		h.wait()
		cleanMethodHelper.ordinary(h, t)
		cleanMethodHelper.reassigned(h, bt, t)
	})
}
