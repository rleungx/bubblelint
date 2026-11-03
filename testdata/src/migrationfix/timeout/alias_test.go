package timeout

import (
	"testing"
	clock "time"
)

func TestTimeAlias(t *testing.T) {
	done := make(chan struct{})
	go func() {
		clock.Sleep(clock.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-clock.After(clock.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}
