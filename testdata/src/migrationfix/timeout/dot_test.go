package timeout

import (
	"testing"
	. "time"
)

func TestTimeDotImport(t *testing.T) {
	done := make(chan struct{})
	go func() {
		Sleep(Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-After(Second): // want "ST101:"
		t.Fatal("timed out")
	}
}
