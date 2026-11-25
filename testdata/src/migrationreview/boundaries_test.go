package migrationreview

import (
	"net/rpc"
	"sync"
	"testing"
	"time"
)

func opaqueRPC() {
	client, _ := rpc.Dial("tcp", "127.0.0.1:1")
	if client != nil {
		client.Close()
	}
}

func timerThenRPC() {
	waitForTimer()
	opaqueRPC()
}

func TestOpaqueWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		timerThenRPC()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestOpaqueSetup(t *testing.T) {
	opaqueRPC()
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForTimer()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestOpaqueCleanup(t *testing.T) {
	t.Cleanup(opaqueRPC)
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForTimer()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestClockOrdering(t *testing.T) {
	before := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForTimer()
	}()
	select {
	case <-done:
		if !time.Now().After(before) {
			t.Fatal("clock did not advance")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func lockedTimer(mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	waitForTimer()
}

func TestMutexWait(t *testing.T) {
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		lockedTimer(&mu)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func neverFinishes() {
	waitForTimer()
	for {
	}
}

func TestUnboundedWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		neverFinishes()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func recursiveTimer(n int) {
	waitForTimer()
	if n > 0 {
		recursiveTimer(n - 1)
	}
}

func TestRecursiveWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		recursiveTimer(2)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestChannelCapturedFromParent(t *testing.T) {
	done := make(chan struct{})
	t.Run("child", func(t *testing.T) {
		go func() {
			defer close(done)
			waitForTimer()
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("worker did not finish")
		}
	})
}
