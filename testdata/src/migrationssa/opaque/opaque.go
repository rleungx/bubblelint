package opaque

import (
	"net"
	"sync/atomic"
	"time"
)

func Touch(flag *atomic.Bool) { flag.Store(true) }

func TouchThrough(ch <-chan *bool) { *<-ch = true }

type Local struct{}

func (*Local) Run() { time.Sleep(time.Millisecond) }

type Remote struct{}

func (*Remote) Run() { _, _ = net.Dial("tcp", "127.0.0.1:1") }

func Forget[T any](*T) {}

func Apply[T any](pointer *atomic.Pointer[T], callback func(*T)) { callback(pointer.Load()) }

func NewFlag() *atomic.Bool { return new(atomic.Bool) }
