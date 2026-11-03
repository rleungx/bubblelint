package blank

import (
	"testing"
	_ "testing/synctest"
	"time"
)

func TestMigration(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}
