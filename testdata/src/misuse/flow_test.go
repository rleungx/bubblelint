package misuse

import (
	"os"
	"testing"
	"testing/synctest"
)

func TestDeferredWaitOutside(t *testing.T) {
	defer synctest.Wait() // want "ST001:"
}

func TestConditionalDeferredWaitOutside(t *testing.T) {
	if testing.Short() {
		defer synctest.Wait() // want "ST001:"
	}
}

func TestDeferredNestedBubble(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		defer synctest.Test(bt, func(*testing.T) {}) // want "ST002:"
		defer bt.Parallel()                          // want "ST003:"
	})
}

func TestDeferredWaitAfterFatal(t *testing.T) {
	defer synctest.Wait() // want "ST001:"
	t.Fatal("runs defers")
}

func TestDeferredWaitAfterPanic(t *testing.T) {
	defer synctest.Wait() // want "ST001:"
	panic("runs defers")
}

func TestDeferredProcessExitSkipsOlderDefers(t *testing.T) {
	defer synctest.Wait()
	defer os.Exit(0)
}

func TestProcessExitSkipsDefers(t *testing.T) {
	defer synctest.Wait()
	os.Exit(0)
}
