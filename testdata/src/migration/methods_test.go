package migration

import (
	"testing"
	"time"
)

type workerStarter struct{}

func (workerStarter) start() { go func() {}() }

func TestWorkerMethodValue(t *testing.T) {
	workerStarter{}.start()
	time.Sleep(time.Millisecond) // want "ST100:"
}

func TestWorkerMethodExpression(t *testing.T) {
	start := workerStarter.start
	start(workerStarter{})
	time.Sleep(time.Millisecond) // want "ST100:"
}
