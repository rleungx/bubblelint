package timeout

import (
	"testing"
	"time"
)

func TestSelect(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestSend(t *testing.T) {
	result := make(chan int)
	go func() {
		time.Sleep(time.Millisecond)
		result <- 42
	}()
	select {
	case got := <-result:
		if got != 42 {
			t.Fatalf("got %d, want 42", got)
		}
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestCommaOK(t *testing.T) {
	result := make(chan bool)
	go func() { close(result) }()
	select {
	case got, ok := <-result:
		if got || ok {
			t.Errorf("got (%t, %t), want (false, false)", got, ok)
		}
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestAssignment(t *testing.T) {
	result := make(chan string, 1)
	got, ok := "", false
	go func() { result <- "ready" }()
	select {
	case got, ok = <-result:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
	if !ok || got != "ready" {
		t.Errorf("got (%q, %t)", got, ok)
	}
}

func TestTimeoutFirst(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	case <-done:
		t.Log("finished")
	}
}

func TestAccumulatedDelay(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Millisecond)
		time.Sleep(-4 * time.Millisecond)
		time.Sleep(3 * time.Millisecond)
		time.Sleep(0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Millisecond): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestConstantDelay(t *testing.T) {
	const delay time.Duration = 2 * time.Millisecond
	const timeout = 2 * delay
	done := make(chan struct{})
	go func() {
		time.Sleep(delay)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestPreviouslyJoinedWorkers(t *testing.T) {
	first := make(chan struct{})
	last := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		close(first)
	}()
	go func() {
		time.Sleep(2 * time.Millisecond)
		close(last)
	}()
	<-first
	select {
	case <-last:
		t.Log("all workers finished")
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestBlankReceive(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case _ = <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}

func TestEarlyReturn(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
		return
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timed out")
	}
}
