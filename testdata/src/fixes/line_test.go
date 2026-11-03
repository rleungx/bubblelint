package fixes

import (
	"testing"
	"testing/synctest"
)

func TestLineDirective(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
//line remapped_test.go:100
		bt.Parallel() // want "ST003:"
	})
}
