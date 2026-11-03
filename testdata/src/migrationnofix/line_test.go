package migrationnofix

import (
	"testing"
	"time"
)

func TestRemappedSource(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
//line mapped_migration_test.go:100
	time.Sleep(time.Second) // want "ST100:"
	<-done
}
