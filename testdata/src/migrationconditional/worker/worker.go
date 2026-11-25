package worker

import (
	"net"
	"time"
)

func Delay() {
	time.Sleep(time.Millisecond)
}

func Fetch() bool {
	_, err := net.Dial("tcp", "127.0.0.1:1")
	return err == nil
}

func MaybeFetch(enabled bool) {
	if enabled {
		Fetch()
	}
}

func Probe(enabled bool) bool {
	return enabled && Fetch()
}

func AlwaysFetch(flag bool) {
	if flag {
		Delay()
	} else {
		time.Sleep(2 * time.Millisecond)
	}
	Fetch()
}
