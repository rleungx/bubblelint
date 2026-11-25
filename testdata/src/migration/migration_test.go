package migration

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	tm "time"
)

func TestSleep(t *testing.T) {
	go func() {}()
	tm.Sleep(tm.Second)   // want "ST100:"
	(tm.Sleep)(tm.Second) // want "ST100:"
	pause := tm.Sleep
	pause(tm.Second) // want "ST100:"
}

func TestHelper(t *testing.T) {
	start()
	pause()
}

func start() { go func() {}() }
func pause() { tm.Sleep(tm.Second) } // want "ST100:"

func TestAfterFunc(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	context.AfterFunc(ctx, func() {})
	cancel()
	tm.Sleep(tm.Millisecond)
}

func TestTimerCallback(t *testing.T) {
	timer := tm.AfterFunc(0, func() { tm.Sleep(tm.Nanosecond) })
	defer timer.Stop()
	tm.Sleep(tm.Millisecond) // want "ST100:"
}

func TestChannel(t *testing.T) {
	done := make(chan bool)
	select {
	case <-done:
	case <-tm.After(tm.Millisecond):
	}
	select {
	case _, ok := <-done:
		_ = ok
	case <-tm.After(tm.Millisecond):
	}
}

func TestSubtests(t *testing.T) {
	t.Run("async", func(t *testing.T) {
		go func() {}()
		tm.Sleep(tm.Millisecond) // want "ST100:"
	})
	t.Run("other", func(t *testing.T) {
		tm.Sleep(tm.Nanosecond)
	})
}

func TestMixed(t *testing.T) {
	go func() {}()
	synctest.Test(t, func(t *testing.T) {
		go func() {}()
		tm.Sleep(tm.Second)
	})
	tm.Sleep(tm.Second) // want "ST100:"
}

func TestWaitGroup(t *testing.T) {
	var wg sync.WaitGroup
	wg.Go(func() { tm.Sleep(tm.Nanosecond) })
	tm.Sleep(tm.Second) // want "ST100:"
	wg.Wait()
}

func TestBoundedChannel(t *testing.T) {
	done := make(chan bool)
	go func() {
		tm.Sleep(tm.Millisecond)
		done <- true
	}()
	select {
	case <-done:
	case <-tm.After(tm.Second): // want "ST101:"
		t.Fatal("worker did not finish")
	}
}
