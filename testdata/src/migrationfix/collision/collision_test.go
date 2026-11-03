package collision

import (
	"testing"
	"time"
)

var synctest int

func TestMigration(t *testing.T) {
	synctest1 := 1
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
	if synctest1 != 1 {
		t.Fatal("local value changed")
	}
}
