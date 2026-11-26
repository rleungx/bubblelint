package worker

import (
	"net"
	"time"
)

func Delay() {
	time.Sleep(time.Millisecond)
}

func Fetch() {
	conn, _ := net.Dial("tcp", "127.0.0.1:1")
	if conn != nil {
		conn.Close()
	}
}

func Run(callback func()) {
	callback()
}

func UnknownCallback() func() {
	callbacks := []func(){Delay}
	return callbacks[0]
}

type Sleeper interface {
	Wait()
}

type Local struct{}

func (Local) Wait() {
	Delay()
}

type Remote struct{}

func (Remote) Wait() {
	Delay()
	Fetch()
}

func NewSleeper() Sleeper {
	return Local{}
}

func RunGenerated(options *GeneratedOptions) {
	if options.GetRemote() {
		Fetch()
	} else {
		Delay()
	}
}
