package disabled

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestDisabled(t *testing.T) {
	synctest.Wait()
	synctest.Test(t, func(t *testing.T) { t.Parallel() })
	go func() {}()
	time.Sleep(time.Second)
}
