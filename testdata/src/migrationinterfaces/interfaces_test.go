package migrationinterfaces

import (
	"net"
	"testing"
	"time"

	"migrationinterfaces/guard"
)

func fetch() { _, _ = net.Dial("tcp", "127.0.0.1:1") }
func pause() { time.Sleep(time.Millisecond) }

type localReporter struct{}

func (*localReporter) Helper() {}
func (*localReporter) Fail()   { fetch() }

type remoteReporter struct{}

func (*remoteReporter) Helper() { fetch() }
func (*remoteReporter) Fail()   { fetch() }

type otherSignature struct{}

func (*otherSignature) Helper(int) { fetch() }
func (*otherSignature) Fail()      { fetch() }

type privateReporter struct{}

func (*privateReporter) hidden() { fetch() }

func TestTestingReceiver(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(t, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestConstructedTestingReceiver(t *testing.T) {
	checks := guard.New(t)
	done := make(chan struct{})
	go func() { defer close(done); pause(); checks.Check(true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestLocalReceiver(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(&localReporter{}, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestHelperHasNetworkEffects(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(&remoteReporter{}, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestFailureHasNetworkEffects(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(&localReporter{}, false) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestUnknownFailure(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(&localReporter{}, testing.Short()) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestDifferentHelperSignature(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(&otherSignature{}, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestMatchingHelperSignature(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Signature(&otherSignature{}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestPrivateMethodIdentity(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Private(&privateReporter{}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestUnsupportedMethodSignature(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.StructSignature(&otherSignature{}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestNilInterface(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(nil, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestTypedNilInterface(t *testing.T) {
	var reporter *localReporter = nil
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.CheckNil(reporter) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func chooseReporter() guard.Reporter {
	if testing.Short() {
		return &remoteReporter{}
	}
	return &localReporter{}
}

func TestUnknownConcreteType(t *testing.T) {
	reporter := chooseReporter()
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(reporter, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestConstructorMutation(t *testing.T) {
	checks := guard.New(&localReporter{})
	checks.Reporter = &remoteReporter{}
	done := make(chan struct{})
	go func() { defer close(done); pause(); checks.Check(true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestBasicFormatting(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Print(t, "worker finished") }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

type stringer struct{}

func (*stringer) String() string { fetch(); return "remote" }

func TestFormattingMethod(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Print(t, &stringer{}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:.*formatting"
		t.Fatal("worker did not finish")
	}
}

type embeddedRemote struct{ *remoteReporter }

func TestPromotedReceiver(t *testing.T) {
	reporter := &embeddedRemote{remoteReporter: &remoteReporter{}}
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(reporter, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

type mutableReporter struct{ remote bool }

func (r *mutableReporter) Helper()      { r.remote = true }
func (r *mutableReporter) Remote() bool { return r.remote }
func (*mutableReporter) Fail()          { fetch() }

func TestHelperMutation(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.CheckState(&mutableReporter{}) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestLocalTypeIdentity(t *testing.T) {
	type localReporter struct{ *remoteReporter }
	reporter := &localReporter{remoteReporter: &remoteReporter{}}
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(reporter, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestLocalTypedNil(t *testing.T) {
	type localReporter struct{ *remoteReporter }
	var reporter *localReporter = nil
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(reporter, true) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestReceivedArgument(t *testing.T) {
	values := make(chan bool, 1)
	values <- true
	value := <-values
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(t, value) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST102:"
		t.Fatal("worker did not finish")
	}
}

func TestReceivedFailureArgument(t *testing.T) {
	values := make(chan bool, 1)
	values <- true
	value := <-values
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Check(&localReporter{}, value) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestAssertedNilPointer(t *testing.T) {
	var pointer *guard.Marker = nil
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.AssertedNil(pointer, fetch) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

type firstBool bool
type secondBool bool

func TestDifferentDynamicBooleanTypes(t *testing.T) {
	const left firstBool = false
	const right secondBool = false
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.Equal(left, right, fetch) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration blocked: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestDistinctAddresses(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.EqualAddress(&guard.Flag{}, &guard.Flag{}, fetch) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven: conditional path: external network I/O"
		t.Fatal("worker did not finish")
	}
}

func TestGenericNilPointer(t *testing.T) {
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.GenericNil[*guard.Marker](nil) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}

func TestGenericInterfaceEquality(t *testing.T) {
	const left firstBool = false
	const right secondBool = false
	done := make(chan struct{})
	go func() { defer close(done); pause(); guard.GenericEqual[any](left, right, fetch) }()
	select {
	case <-done:
	case <-time.After(time.Second): // want "migration unproven:"
		t.Fatal("worker did not finish")
	}
}
