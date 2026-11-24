package migrationsuggestions

import (
	"testing"
	"testing/synctest"
	"time"
)

var _ = synctest.Test

func TestSubtest(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		t.Helper()
		done := make(chan struct{})
		go func() { close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second): // want "ST101:"
			t.Fatal("timeout")
		}
	})
}

var referenced = TestReferenced

func TestReferenced(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timeout")
	}
}

//go:noinline
func TestDirective(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}

func TestUnnamed(_ *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}

func TestShadowedImport(synctest *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}
