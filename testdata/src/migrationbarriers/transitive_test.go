package migrationbarriers

import (
	"net"
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func dialHelper()  { _, _ = net.Dial("tcp", "localhost:12345") }
func wrappedDial() { dialHelper() }

func TestNetworkBehindHelper(t *testing.T) {
	go wrappedDial()
	time.Sleep(time.Second)
}

// The effect is discovered after visiting the recursive back edge. Both
// entry points must get the same answer regardless of traversal order.
func cycleA(stop bool) {
	if stop {
		return
	}
	cycleB(stop)
	dialHelper()
}
func cycleB(stop bool) { cycleA(stop) }

func TestRecursiveNetworkA(t *testing.T) {
	go cycleA(true)
	time.Sleep(time.Second)
}

func TestRecursiveNetworkB(t *testing.T) {
	go cycleB(true)
	time.Sleep(time.Second)
}

func TestAliasedHelper(t *testing.T) {
	start := wrappedDial
	go start()
	time.Sleep(time.Second)
}

type networkStore struct{}

func (networkStore) request() { wrappedDial() }

func TestMethodHelper(t *testing.T) {
	var client networkStore
	go client.request()
	time.Sleep(time.Second)
}

func TestNamedNetworkCallback(t *testing.T) {
	time.AfterFunc(time.Millisecond, wrappedDial)
	time.Sleep(time.Second)
}

func TestUnusedNetworkClosure(t *testing.T) {
	unused := func() { dialHelper() }
	_ = unused
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestUnreachableNetwork(t *testing.T) {
	if false {
		dialHelper()
	}
	for range 0 {
		dialHelper()
	}
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
	return
	dialHelper()
}

func networkCondition() bool {
	dialHelper()
	return true
}

func TestShortCircuitedNetwork(t *testing.T) {
	_ = false && networkCondition()
	_ = true || networkCondition()
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func registerFinalizer() {
	p := new([32]byte)
	runtime.SetFinalizer(p, func(*[32]byte) {})
}

func TestWrappedFinalizer(t *testing.T) {
	registerFinalizer()
	go func() {}()
	time.Sleep(time.Second)
}

func registerCleanup() {
	p := new([32]byte)
	runtime.AddCleanup(p, func(int) {}, 0)
}

func TestWrappedCleanup(t *testing.T) {
	registerCleanup()
	go func() {}()
	time.Sleep(time.Second)
}

func TestFinalizerCallback(t *testing.T) {
	done := make(chan struct{})
	p := new([32]byte)
	runtime.SetFinalizer(p, func(*[32]byte) { close(done) })
	runtime.GC()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func spin() {
	for {
	}
}

func recurse() { recurse() }

func TestCPUWatchdog(t *testing.T) {
	done := make(chan struct{})
	go func() {
		spin()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("computation did not finish")
	}
}

func TestRecursiveWatchdog(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		recurse()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("computation did not finish")
	}
}

func TestUnknownProducer(t *testing.T) {
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func TestBusyClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	var wg sync.WaitGroup
	wg.Go(func() {
		for time.Now().Before(end) {
		}
	})
	time.Sleep(time.Second)
	wg.Wait()
}

func TestCooperativeClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for time.Now().Before(end) {
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(2 * time.Second) // want "ST100:"
}

func TestConditionalSleepClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for time.Now().Before(end) {
			if testing.Short() {
				time.Sleep(time.Millisecond)
			}
		}
	}()
	time.Sleep(2 * time.Second)
}

func TestZeroSleepClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for time.Now().Before(end) {
			time.Sleep(0)
		}
	}()
	time.Sleep(2 * time.Second)
}

func TestClockReadWithoutCycle(t *testing.T) {
	go func() { _ = time.Now() }()
	time.Sleep(time.Second) // want "ST100:"
}

func TestMisuseBehindBarrier(t *testing.T) {
	wrappedDial()
	synctest.Wait() // want "ST001:"
	synctest.Test(t, func(t *testing.T) {
		t.Parallel()                            // want "ST003:"
		synctest.Test(t, func(t *testing.T) {}) // want "ST002:"
	})
}
