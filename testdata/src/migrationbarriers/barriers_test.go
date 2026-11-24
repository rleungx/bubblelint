package migrationbarriers

import (
	"context"
	"net"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

type store interface {
	Get(context.Context, string) (string, error)
}

type client struct{ store }

// A helper can wait for external state before calling an opaque client. The
// sleep is not necessarily synchronization that virtual time can replace.
func waitForState(ctx context.Context, c *client) {
	time.Sleep(time.Second)
	_, _ = c.Get(ctx, "key")
}

func TestOpaqueHelper(t *testing.T) {
	go func() {}()
	waitForState(t.Context(), &client{})
}

func TestOpaqueTimeout(t *testing.T) {
	c := &client{}
	_, _ = c.Get(t.Context(), "key")
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func TestMethodAlias(t *testing.T) {
	c := &client{}
	get := c.Get
	go func() {}()
	time.Sleep(time.Second)
	_, _ = get(t.Context(), "key")
}

func TestMethodExpression(t *testing.T) {
	get := store.Get
	go func() {}()
	time.Sleep(time.Second)
	_, _ = get(nil, t.Context(), "key")
}

func sharedWait() {
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestOpaqueCallerFirst(t *testing.T) {
	c := &client{}
	_, _ = c.Get(t.Context(), "key")
	sharedWait()
}

func TestPureCaller(t *testing.T) {
	sharedWait()
}

func TestNetworkWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		conn, _ := net.Dial("tcp", "localhost:12345")
		_ = conn.Close()
		close(done)
	}()
	time.Sleep(time.Second)
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func TestHTTPAfterSleep(t *testing.T) {
	go func() {}()
	time.Sleep(time.Second)
	_, _ = http.Get("http://localhost")
}

func TestConcreteHTTPClient(t *testing.T) {
	c := &http.Client{}
	go func() {}()
	time.Sleep(time.Second)
	_, _ = c.Do(nil)
}

type memoryStore struct{}

func (memoryStore) Get(context.Context, string) (string, error) { return "", nil }

func TestConcreteStore(t *testing.T) {
	c := memoryStore{}
	_, _ = c.Get(t.Context(), "key")
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestKnownInterfaceStore(t *testing.T) {
	var c store = memoryStore{}
	_, _ = c.Get(t.Context(), "key")
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestKnownInterfaceAlias(t *testing.T) {
	c := store(memoryStore{})
	get := c.Get
	_, _ = get(t.Context(), "key")
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestKnownInterfaceMethodExpression(t *testing.T) {
	_, _ = store.Get(memoryStore{}, t.Context(), "key")
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestPureTimeout(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // want "ST101:"
	}
}

func TestAddressParsing(t *testing.T) {
	_ = net.ParseIP("127.0.0.1")
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestInMemoryNetworkValues(t *testing.T) {
	left, right := net.Pipe()
	_, _ = left, right
	var buffers net.Buffers
	_, _ = buffers.Read(nil)
	header := http.Header{}
	_ = header.Get("Content-Type")
	go func() {}()
	time.Sleep(time.Second) // want "ST100:"
}

func TestMisuseStillReported(t *testing.T) {
	_, _ = net.Dial("tcp", "localhost:12345")
	synctest.Wait() // want "ST001:"
	synctest.Test(t, func(t *testing.T) {
		t.Parallel()                            // want "ST003:"
		synctest.Test(t, func(t *testing.T) {}) // want "ST002:"
	})
}
