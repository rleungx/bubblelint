package migrationsuggestions

import (
	"testing"
	"time"
)

func TestRemappedSource(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
//line mapped_timeout_test.go:100
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}
