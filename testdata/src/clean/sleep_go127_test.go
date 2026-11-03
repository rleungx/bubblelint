//go:build go1.27

package clean

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSynctestSleepInside(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		synctest.Sleep(time.Nanosecond)
		sleep := synctest.Sleep
		sleep(time.Nanosecond)
		bt.Cleanup(func() { synctest.Sleep(time.Nanosecond) })
		t.Run("ordinary T", func(sub *testing.T) {
			sub.Cleanup(func() { synctest.Sleep(time.Nanosecond) })
		})
	})
}

func TestShadowedSynctestSleep(t *testing.T) {
	synctest := fake{}
	synctest.Sleep(time.Nanosecond)
	sleep := synctest.Sleep
	sleep(time.Nanosecond)
}
