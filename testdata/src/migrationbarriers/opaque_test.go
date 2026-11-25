package migrationbarriers

import (
	"net/rpc"
	"testing"
	"time"
)

func callImportedWorker() {
	_, _ = rpc.Dial("tcp", "127.0.0.1:1")
}

func TestOpaqueExternalSleep(t *testing.T) {
	go callImportedWorker()
	time.Sleep(time.Second)
}

func TestOpaqueExternalSetup(t *testing.T) {
	callImportedWorker()
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

type networkLabel struct{}

func (networkLabel) String() string {
	callImportedWorker()
	return "label"
}

func TestImplicitFormattingCallback(t *testing.T) {
	go func() {}()
	time.Sleep(time.Second)
	t.Log(networkLabel{})
}
