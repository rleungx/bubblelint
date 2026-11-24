package migrationbarriers

import (
	"net"
	"testing"
	"time"
)

func TestSliceCallback(t *testing.T) {
	jobs := []func(){func() { _, _ = net.Dial("tcp", "localhost:12345") }}
	go jobs[0]()
	time.Sleep(time.Second)
}

func TestMapCallback(t *testing.T) {
	jobs := map[string]func(){"dial": wrappedDial}
	go jobs["dial"]()
	time.Sleep(time.Second)
}

func TestFieldCallback(t *testing.T) {
	job := struct{ run func() }{wrappedDial}
	go job.run()
	time.Sleep(time.Second)
}

func callUnknown(job func()) { job() }

func TestUnknownCallback(t *testing.T) {
	go callUnknown(wrappedDial)
	time.Sleep(time.Second)
}

func clockNow() time.Time       { return time.Now() }
func before(end time.Time) bool { return clockNow().Before(end) }

func TestHelperClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for before(end) {
		}
	}()
	time.Sleep(2 * time.Second)
}

func TestCooperativeHelperClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for before(end) {
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(2 * time.Second) // want "ST100:"
}

func TestFiniteClockLoop(t *testing.T) {
	done := make(chan struct{})
	go func() {
		for range 2 {
			_ = time.Now()
		}
		close(done)
	}()
	time.Sleep(time.Second) // want "ST100:"
	<-done
}

func TestFiniteHelperClockLoop(t *testing.T) {
	go func() {
		for range []int{1, 2} {
			_ = clockNow()
		}
	}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestNestedBusyClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for range 2 {
			for before(end) {
			}
		}
	}()
	time.Sleep(2 * time.Second)
}

func TestRepeatedFiniteClockLoop(t *testing.T) {
	go func() {
		for {
			for range 2 {
				_ = time.Now()
			}
		}
	}()
	time.Sleep(time.Second)
}

func TestShortCircuitedClockLoop(t *testing.T) {
	end := time.Now().Add(time.Second)
	go func() {
		for i := 0; i < 2; i++ {
			_ = false && time.Now().Before(end)
			_ = true || before(end)
		}
	}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestHelperSetup(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
		t.Fatal("timeout")
	}
}

func TestFiniteClockContinue(t *testing.T) {
	go func() {
		for range 2 {
			_ = clockNow()
			continue
		}
	}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestRestartedFiniteClockRange(t *testing.T) {
	go func() {
	again:
		for range 2 {
			_ = clockNow()
			goto again
		}
	}()
	time.Sleep(time.Second)
}

func TestAsyncClockRead(t *testing.T) {
	go func() {
		for i := 0; i < 2; i++ {
			go func() { _ = clockNow() }()
		}
	}()
	time.Sleep(time.Second) // want "ST100:"
}

func networkJob() func()         { return func() { wrappedDial() } }
func pairJob() (int, func())     { return 0, wrappedDial }
func clockJob() func() time.Time { return clockNow }

func TestFactoryNetworkCallback(t *testing.T) {
	go networkJob()()
	time.Sleep(time.Second)
}

func TestFactoryPairCallback(t *testing.T) {
	_, job := pairJob()
	go job()
	time.Sleep(time.Second)
}

func TestFactoryDeclarationCallback(t *testing.T) {
	var _, job = pairJob()
	go job()
	time.Sleep(time.Second)
}

func TestFactoryClockCallback(t *testing.T) {
	now := clockJob()
	end := time.Now().Add(time.Second)
	go func() {
		for now().Before(end) {
		}
	}()
	time.Sleep(2 * time.Second)
}

func pureJob() (int, func()) { return 0, func() {} }

func TestFactoryPureCallback(t *testing.T) {
	_, job := pureJob()
	go job()
	time.Sleep(time.Second) // want "ST100:"
}

func TestReassignedFactoryCallback(t *testing.T) {
	_, job := pureJob()
	job = wrappedDial
	go job()
	time.Sleep(time.Second)
}

func genericJob[F ~func()](job F) {
	go job()
	time.Sleep(time.Second)
}

func TestGenericFunctionValue(t *testing.T) {
	genericJob(func() {})
}
