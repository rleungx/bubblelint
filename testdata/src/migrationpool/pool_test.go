package migrationpool

import (
	"net"
	"sync"
	"testing"
	"time"

	"migrationpool/store"
)

func fetch() { _, _ = net.Dial("tcp", "127.0.0.1:1") }

func pause() { time.Sleep(time.Millisecond) }

func TestPutValue(t *testing.T) {
	pool := new(sync.Pool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		pool.Put(new(int))
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestPutFunction(t *testing.T) {
	pool := &sync.Pool{New: func() any { fetch(); return nil }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		pool.Put(fetch)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestImportedPut(t *testing.T) {
	pool := new(sync.Pool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		store.Put(pool, new(int))
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestMethodExpression(t *testing.T) {
	pool := new(sync.Pool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		(*sync.Pool).Put(pool, new(int))
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

type putter interface{ Put(any) }

func TestConcreteInterface(t *testing.T) {
	var pool putter = new(sync.Pool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		pool.Put(new(int))
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestGetCallback(t *testing.T) {
	pool := &sync.Pool{New: func() any { fetch(); return nil }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		pool.Get()
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:.*pool's New callback"
		t.Fatal("worker did not finish")
	}
}

func TestGetAfterPut(t *testing.T) {
	pool := new(sync.Pool)
	pool.Put(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		if pool.Get() != true {
			fetch()
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

type state struct{ enabled bool }

func (s *state) run() {
	if s.enabled {
		fetch()
	} else {
		pause()
	}
}

func TestPublishedPointer(t *testing.T) {
	pool, s := new(sync.Pool), new(state)
	pool.Put(s)
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestPublishedCapture(t *testing.T) {
	pool, s := new(sync.Pool), new(state)
	pool.Put(func() { s.enabled = true })
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestImportedPublication(t *testing.T) {
	pool, s := new(sync.Pool), new(state)
	store.Put(pool, s)
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type Pool struct{}

func (*Pool) Put(any) { fetch() }

func TestSameNamedMethod(t *testing.T) {
	pool := new(Pool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pause()
		pool.Put(new(int))
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}
