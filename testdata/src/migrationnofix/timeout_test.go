package migrationnofix

import (
	"testing"
	"time"
)

func TestTimeoutEqualToWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutShorterThanWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutShorterThanTotalSleeps(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Second)
		time.Sleep(-3 * time.Second)
		time.Sleep(2 * time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutWorkerSumOverflow(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(1 << 62)
		time.Sleep(1 << 62)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutSaturatedTimers(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(1<<63 - 2)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1<<63 - 1): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutAfterSaturatedClock(t *testing.T) {
	time.Sleep(1<<63 - 1)
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Nanosecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Nanosecond): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutVariable(t *testing.T) {
	limit := time.Second
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(limit): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutWorkerVariableDelay(t *testing.T) {
	delay := time.Millisecond
	done := make(chan struct{})
	go func() {
		time.Sleep(delay)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutZero(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(0): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutNegative(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(-time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutDefaultCase(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	default:
		t.Fatal("not ready")
	}
}

func TestTimeoutExtraCase(t *testing.T) {
	done := make(chan struct{})
	extra := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-extra:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutSendCase(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	select {
	case done <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestTimeoutResultBinding(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case now := <-time.After(time.Second): // want "ST101:"
		t.Fatal(now)
	}
}

func TestTimeoutBlankResultBinding(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case _ = <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutOutstandingWorker(t *testing.T) {
	done := make(chan struct{})
	other := make(chan struct{})
	go func() { close(done) }()
	go func() {
		time.Sleep(time.Second)
		close(other)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
	<-other
}

func TestTimeoutActionAfterSignal(t *testing.T) {
	done := make(chan struct{})
	go func() {
		close(done)
		time.Sleep(time.Second)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutHelper(t *testing.T) {
	done := make(chan struct{})
	go finish(done)
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutChannelAlias(t *testing.T) {
	done := make(chan struct{})
	alias := done
	go func() { close(done) }()
	select {
	case <-alias:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutWithoutWorker(t *testing.T) {
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutFunctionAlias(t *testing.T) {
	after := time.After
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-after(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutUnsupportedBranch(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
		t.Parallel()
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestTimeoutRepeatedReceive(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out again")
	}
}
