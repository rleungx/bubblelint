package migrationreview

import (
	"testing"
	"time"
)

type result struct{ values []int }
type resultChannel chan result

func loadResult() result {
	waitForTimer()
	return result{values: []int{1, 2}}
}

func TestResultSend(t *testing.T) {
	results := make(resultChannel, 1)
	output := results
	go func() {
		value := loadResult()
		output <- value
	}()
	select {
	case value := <-results:
		if len(value.values) != 2 {
			t.Fatal("unexpected result")
		}
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not return a result")
	}
}

func TestFinalClose(t *testing.T) {
	done := make(chan struct{})
	go func() {
		waitThroughHelper()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

type gateWaiter interface{ wait() }
type gatedTask struct{ gate *localGate }

func (task *gatedTask) result() result {
	task.gate.wait()
	return result{values: []int{1}}
}

type opaqueTask struct{ gate gateWaiter }

func (task *opaqueTask) result() result {
	task.gate.wait()
	return result{}
}

func TestOpaqueGate(t *testing.T) {
	gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	task := &opaqueTask{gate: gate}
	results := make(chan result, 1)
	go func() { results <- task.result() }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second): // want "ST102:"
		close(gate.release)
		t.Fatal("worker did not reach the gate")
	}
	close(gate.release)
	<-results
}

func TestInjectedGateWithResult(t *testing.T) {
	gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	task := &gatedTask{}
	task.gate = gate
	results := make(chan result, 1)
	go func() { results <- task.result() }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second): // want "ST102:"
		close(gate.release)
		t.Fatal("worker did not reach the gate")
	}
	close(gate.release)
	output := results
	value := <-output
	_ = value
}

func TestUnrelatedGate(t *testing.T) {
	gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	other := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	task := &gatedTask{gate: other}
	results := make(chan result, 1)
	go func() { results <- task.result() }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		close(other.release)
		t.Fatal("unrelated channel never signaled")
	}
	close(other.release)
	<-results
}

func TestNegativeAssertion(t *testing.T) {
	gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	task := &gatedTask{gate: gate}
	results := make(chan result, 1)
	go func() { results <- task.result() }()
	select {
	case <-results:
		t.Fatal("worker should still be blocked")
	case <-time.After(time.Millisecond):
	}
	close(gate.release)
	<-results
}

func TestGateAssignedAfterTimeout(t *testing.T) {
	gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	task := &gatedTask{}
	results := make(chan result, 1)
	go func() { results <- task.result() }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		t.Fatal("worker cannot access the gate yet")
	}
	task.gate = gate
	close(gate.release)
	<-results
}

func TestUnreachableJoin(t *testing.T) {
	gate := &localGate{entered: make(chan struct{}), release: make(chan struct{})}
	task := &gatedTask{gate: gate}
	results := make(chan result, 1)
	go func() { results <- task.result() }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		close(gate.release)
		t.Fatal("worker did not reach the gate")
	}
	close(gate.release)
	return
	<-results
}

func TestUnreachableTimeout(t *testing.T) {
	results := make(chan result, 1)
	go func() { results <- loadResult() }()
	<-results
	return
	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("unreachable timeout")
	}
}

func TestUnreachableFinalSignal(t *testing.T) {
	results := make(chan result, 1)
	go func() {
		value := loadResult()
		_ = value
		return
		results <- value
	}()
	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("worker never signals")
	}
}

func networkResult() result {
	waitForNetwork()
	return result{}
}

func TestResultWithTransitiveNetwork(t *testing.T) {
	results := make(chan result, 1)
	go func() { results <- networkResult() }()
	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("network operation did not finish")
	}
}

func TestVerifiedProtocolIsNotReviewCandidate(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

var sharedDone = make(chan struct{})

func TestGlobalChannel(t *testing.T) {
	localAlias := sharedDone
	go func() {
		waitThroughHelper()
		close(localAlias)
	}()
	select {
	case <-localAlias:
	case <-time.After(time.Second):
		t.Fatal("shared channel did not close")
	}
}
