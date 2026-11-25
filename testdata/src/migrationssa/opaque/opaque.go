package opaque

import "sync/atomic"

func Touch(flag *atomic.Bool) { flag.Store(true) }

func TouchThrough(ch <-chan *bool) { *<-ch = true }
