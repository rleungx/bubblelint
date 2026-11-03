package defaults

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestDefault(t *testing.T) {
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}

func TestMisuse(t *testing.T) {
	synctest.Wait() // want "ST001:"
	synctest.Test(t, func(t *testing.T) {
		t.Parallel()                            // want "ST003:"
		synctest.Test(t, func(t *testing.T) {}) // want "ST002:"
	})
}
