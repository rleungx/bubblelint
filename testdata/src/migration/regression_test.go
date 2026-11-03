package migration

import (
	"sync"
	"testing"
	"time"
)

func TestMethodExpressionSubtests(t *testing.T) {
	(*testing.T).Run(t, "direct", func(sub *testing.T) {
		go func() {}()
		time.Sleep(time.Millisecond) // want "ST100:"
	})
	run := (*testing.T).Run
	run(t, "alias", func(sub *testing.T) {
		go func() {}()
		time.Sleep(time.Millisecond) // want "ST100:"
	})
	time.Sleep(time.Nanosecond)
}

func TestMethodExpressionCleanup(t *testing.T) {
	(*testing.T).Cleanup(t, func() {
		go func() {}()
		time.Sleep(time.Millisecond) // want "ST100:"
	})
	cleanup := (*testing.T).Cleanup
	cleanup(t, func() {
		go func() {}()
		time.Sleep(time.Millisecond) // want "ST100:"
	})
	time.Sleep(time.Nanosecond)
}

func TestMethodExpressionWaitGroup(t *testing.T) {
	var wg sync.WaitGroup
	(*sync.WaitGroup).Go(&wg, func() { time.Sleep(time.Nanosecond) })
	time.Sleep(time.Second) // want "ST100:"
	wg.Wait()
}

func TestMethodExpressionWaitGroupAlias(t *testing.T) {
	var wg sync.WaitGroup
	start := (*sync.WaitGroup).Go
	start(&wg, func() { time.Sleep(time.Nanosecond) })
	time.Sleep(time.Second) // want "ST100:"
	wg.Wait()
}
