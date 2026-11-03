package migration

import (
	"testing"
	"time"
)

func flowStart() { go func() {}() }

func TestDeferredWorkStartsAfterSleep(t *testing.T) {
	defer flowStart()
	time.Sleep(time.Millisecond)
}

func TestReturningBranchDoesNotStartFallthroughWork(t *testing.T) {
	if testing.Short() {
		flowStart()
		return
	}
	time.Sleep(time.Millisecond)
}

func TestFallthroughBranchStartsWork(t *testing.T) {
	if testing.Short() {
		flowStart()
	}
	time.Sleep(time.Millisecond) // want "ST100:"
}

func TestReturningElseDoesNotStartFallthroughWork(t *testing.T) {
	if testing.Short() {
		t.Log("continue")
	} else {
		flowStart()
		return
	}
	time.Sleep(time.Millisecond)
}

func TestReturningSwitchDoesNotStartFallthroughWork(t *testing.T) {
	switch {
	case testing.Short():
		flowStart()
		return
	}
	time.Sleep(time.Millisecond)
}

func TestReturningSelectDoesNotStartFallthroughWork(t *testing.T) {
	done := make(chan struct{})
	select {
	case <-done:
		flowStart()
		return
	default:
	}
	time.Sleep(time.Millisecond)
}

func TestReturningLoopDoesNotStartFallthroughWork(t *testing.T) {
	for testing.Short() {
		flowStart()
		return
	}
	time.Sleep(time.Millisecond)
}

func TestBreakingLoopStartsFallthroughWork(t *testing.T) {
	for testing.Short() {
		flowStart()
		break
	}
	time.Sleep(time.Millisecond) // want "ST100:"
}

func TestFalseBranchDoesNotStartWork(t *testing.T) {
	if false {
		flowStart()
	}
	time.Sleep(time.Millisecond)
}

func TestHelperDeferredWorkStartsBeforeCallerSleep(t *testing.T) {
	flowDeferredStart()
	time.Sleep(time.Millisecond) // want "ST100:"
}

func flowDeferredStart() { defer flowStart() }

func TestHelperConditionalDeferStartsBeforeCallerSleep(t *testing.T) {
	flowConditionalDefer()
	time.Sleep(time.Millisecond) // want "ST100:"
}

func flowConditionalDefer() {
	if testing.Short() {
		defer flowStart()
	}
}

func TestDeferredArgumentStartsImmediately(t *testing.T) {
	defer func(int) {}(flowArgument())
	time.Sleep(time.Millisecond) // want "ST100:"
}

func flowArgument() int {
	flowStart()
	return 0
}

func TestDeferredFunctionStartsImmediately(t *testing.T) {
	defer flowFunction()()
	time.Sleep(time.Millisecond) // want "ST100:"
}

func flowFunction() func() {
	flowStart()
	return func() {}
}

func TestDeferredCallsRunInReverseOrder(t *testing.T) {
	defer time.Sleep(time.Millisecond) // want "ST100:"
	defer flowStart()
}

func TestDeferredSleepBeforeDeferredWork(t *testing.T) {
	defer flowStart()
	defer time.Sleep(time.Millisecond)
}

func TestMutuallyExclusiveDefers(t *testing.T) {
	if testing.Short() {
		defer flowStart()
	} else {
		defer time.Sleep(time.Millisecond)
	}
}

func TestDeferredWorkDoesNotRunWhenBlocked(t *testing.T) {
	defer flowStart()
	select {}
	time.Sleep(time.Millisecond)
}

func TestFatalBranchDoesNotStartFallthroughWork(t *testing.T) {
	if testing.Short() {
		flowStart()
		t.Fatal("stop this path")
	}
	time.Sleep(time.Millisecond)
}

func TestSelectAssignmentEvaluatesLHSOnlyInChosenCase(t *testing.T) {
	values := []int{0}
	results := make(chan int)
	select {
	case values[flowArgument()] = <-results:
		return
	default:
	}
	time.Sleep(time.Millisecond)
}

func TestSelectAssignmentEvaluatesEveryLHS(t *testing.T) {
	values := []int{0}
	ok := []bool{false}
	results := make(chan int)
	select {
	case values[0], ok[flowArgument()] = <-results:
		time.Sleep(time.Millisecond) // want "ST100:"
	default:
	}
}

func TestZeroRangeDoesNotStartWork(t *testing.T) {
	for range 0 {
		flowStart()
	}
	time.Sleep(time.Millisecond)
}

func TestZeroRangeDoesNotEvaluateAssignment(t *testing.T) {
	values := []int{0}
	for values[flowArgument()] = range 0 {
	}
	time.Sleep(time.Millisecond)
}

func TestSingleRangeSleepPrecedesWork(t *testing.T) {
	for range 1 {
		time.Sleep(time.Millisecond)
		flowStart()
	}
	time.Sleep(time.Millisecond) // want "ST100:"
}

func TestSingleRangeSleepPrecedesDeferredWork(t *testing.T) {
	for range 1 {
		defer flowStart()
		defer time.Sleep(time.Millisecond)
	}
}

func TestSingleRangeDeferredWorkPrecedesSleep(t *testing.T) {
	for range 1 {
		defer time.Sleep(time.Millisecond) // want "ST100:"
		defer flowStart()
	}
}

func TestCountedLoopDeferredOrderIsConservative(t *testing.T) {
	for i := 0; i < 1; i++ {
		defer flowStart()
		defer time.Sleep(time.Millisecond)
	}
}

func TestLoopDeferredWorkReachesCaller(t *testing.T) {
	flowLoopDeferredStart()
	time.Sleep(time.Millisecond) // want "ST100:"
}

func flowLoopDeferredStart() {
	for i := 0; i < 1; i++ {
		defer flowStart()
	}
}

func TestLoopDeferredTimeoutStillReported(t *testing.T) {
	for i := 0; i < 1; i++ {
		defer flowDeferredTimeout()
	}
}

func flowDeferredTimeout() {
	select {
	case <-make(chan struct{}):
	case <-time.After(time.Second): // want "ST101:"
	}
}
