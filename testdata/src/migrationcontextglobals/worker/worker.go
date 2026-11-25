package worker

import (
	"migrationcontextglobals/settings"
	"time"
)

func Delay() { time.Sleep(settings.Delay) }

func Branch() {
	if settings.Local {
		time.Sleep(time.Millisecond)
	} else {
		time.Sleep(2 * time.Millisecond)
	}
}
