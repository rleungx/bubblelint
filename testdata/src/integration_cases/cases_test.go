package integration_cases

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestOutside(t *testing.T) {
	synctest.Wait() // want "ST001:"
}

func TestInside(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		bt.Parallel()                            // want "ST003:"
		synctest.Test(bt, func(t *testing.T) {}) // want "ST002:"
	})
}

func TestMigration(t *testing.T) {
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}
