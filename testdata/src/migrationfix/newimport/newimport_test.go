package newimport

import (
	"testing"
	"time"
)

func TestClose(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Second)
		close(done)
	}()
	time.Sleep(time.Second)     // want "ST100:"
	time.Sleep(2 * time.Second) // Keep both waits. // want "ST100:"
	<-done
}

func TestSend(t *testing.T) {
	result := make(chan int, 1)
	go func() { result <- 42 }()
	time.Sleep(time.Second) // want "ST100:"
	got := <-result
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestWorkers(t *testing.T) {
	closed := make(chan struct{})
	result := make(chan bool)
	go func() {
		time.Sleep(time.Second)
		close(closed)
	}()
	go func() {
		time.Sleep(2 * time.Second)
		result <- true
	}()
	time.Sleep(time.Second) // want "ST100:"
	<-closed
	got := <-result
	if !got {
		t.Fatal("worker did not finish")
	}
}

func TestConstantDelay(t *testing.T) {
	const delay = time.Second
	time.Sleep(time.Millisecond)
	initial := 1
	result := make(chan string)
	go func() {
		time.Sleep(delay)
		result <- "ready"
	}()
	time.Sleep(delay) // want "ST100:"
	got, ok := <-result
	if !ok || got != "ready" || initial != 1 {
		t.Errorf("got (%q, %t)", got, ok)
	} else {
		t.Log("ready")
	}
	return
}
