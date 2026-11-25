package defaults

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestDefault(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second)
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func TestMisuse(t *testing.T) {
	synctest.Wait() // want "ST001:"
	synctest.Test(t, func(t *testing.T) {
		t.Parallel()                            // want "ST003:"
		synctest.Test(t, func(t *testing.T) {}) // want "ST002:"
	})
}
