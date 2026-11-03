package clean

import "testing/synctest"

// Production functions with Test prefixes are not testing entry points.
func TestNotAnEntry() { synctest.Wait() }
