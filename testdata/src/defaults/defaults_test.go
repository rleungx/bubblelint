package defaults

import (
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	go func() {}()
	time.Sleep(time.Second)
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
