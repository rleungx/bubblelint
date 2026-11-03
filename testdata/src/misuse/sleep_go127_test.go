//go:build go1.27

package misuse

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSynctestSleepOutside(t *testing.T) {
	synctest.Sleep(time.Nanosecond)    // want "ST001: synctest.Sleep"
	go synctest.Sleep(time.Nanosecond) // want "ST001: synctest.Sleep"
	sleep := synctest.Sleep
	sleep(time.Nanosecond) // want "ST001: synctest.Sleep"
	t.Cleanup(func() {
		synctest.Sleep(time.Nanosecond) // want "ST001: synctest.Sleep"
	})
	synctest.Test(t, func(bt *testing.T) {
		t.Cleanup(func() {
			synctest.Sleep(time.Nanosecond) // want "ST001: synctest.Sleep"
		})
	})
}
