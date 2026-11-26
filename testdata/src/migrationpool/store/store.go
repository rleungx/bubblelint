package store

import "sync"

func Put(pool *sync.Pool, value any) { pool.Put(value) }
