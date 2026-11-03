package migrationnofix

import (
	"testing"
	"time"
)

var shared bool
var globalDone = make(chan struct{})
var referenced = TestReferenced

type label int

func (label) String() string    { return "label" }
func finish(done chan struct{}) { close(done) }

func TestHelper(t *testing.T) {
	done := make(chan struct{})
	go finish(done)
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestGlobalCapture(t *testing.T) {
	done := make(chan struct{})
	go func() { shared = true; close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestGlobalChannel(t *testing.T) {
	go func() { close(globalDone) }()
	time.Sleep(time.Second) // want "ST100:"
	<-globalDone
}

func TestMutableCapture(t *testing.T) {
	value := 0
	done := make(chan struct{})
	go func() { value++; close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
	if value != 1 {
		t.Fatal("bad result")
	}
}

func TestUnjoined(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
}

func TestEarlyFatal(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	t.Fatal("stop before join")
	<-done
}

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

func TestLoop(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	for range 1 {
		<-done
	}
}

func TestDefer(t *testing.T) {
	defer func() {}()
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestParallel(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestDeadline(t *testing.T) {
	_, _ = t.Deadline()
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestCleanup(t *testing.T) {
	t.Cleanup(func() {})
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestWallClock(t *testing.T) {
	start := time.Now()
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
	t.Log(start)
}

func TestStringer(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
	t.Log(label(1))
}

func TestWorkerAfterClose(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done); time.Sleep(time.Second) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestChannelAlias(t *testing.T) {
	done := make(chan struct{})
	alias := done
	go func() { close(alias) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestReassignedChannel(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	done = make(chan struct{})
	<-done
}

func TestSharedChannel(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestReferenced(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestShadowedParameterType(testing *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestNestedWorkerInIf(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
	if true {
		nested := make(chan struct{})
		go func() { close(nested) }()
		<-nested
	}
}

func TestNestedWorkerInBlock(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
	{
		nested := make(chan struct{})
		go func() { close(nested) }()
		<-nested
	}
}

//go:noinline
func TestCompilerDirective(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}
