package worker

import (
	"net"
	"testing"
	"time"
)

func Delay() { time.Sleep(time.Millisecond) }

func Fetch() { _, _ = net.Dial("tcp", "127.0.0.1:1") }

func MaybeFetch(remote bool) {
	if remote {
		Fetch()
	} else {
		Delay()
	}
}

func WithBackend(backend *bool) {
	if backend != nil {
		Fetch()
	} else {
		Delay()
	}
}

func Run(callback func()) { callback() }

type Task struct{ Remote bool }

func (t *Task) Run() {
	if t.Remote {
		Fetch()
	} else {
		Delay()
	}
}

func NewTask(remote bool) *Task { return &Task{Remote: remote} }

func BothBranches(remote bool) {
	if remote {
		Fetch()
	} else {
		MaybeFetch(true)
	}
}

func Recursive(remote bool) {
	if remote {
		Recursive(remote)
	} else {
		Delay()
	}
}

func RegisterCleanup(t *testing.T, callback func()) { t.Cleanup(callback) }

func NestedReturn(a, b bool) {
	if a {
		if b {
			return
		}
	}
	Fetch()
}
