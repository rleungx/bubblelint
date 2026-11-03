//go:build go1.24

package migrationnofix

import (
	"testing"
	"time"
)

func TestEarlierLanguageVersion(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}
