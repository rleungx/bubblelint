package misuse

import (
	"testing"
	st "testing/synctest"
)

func TestOutside(t *testing.T) {
	st.Wait()    // want "ST001:"
	go st.Wait() // want "ST001:"
	wait := st.Wait
	wait()      // want "ST001:"
	(st.Wait)() // want "ST001:"
	outsideHelper()
	t.Run("subtest", func(t *testing.T) {
		st.Wait() // want "ST001:"
	})
	t.Cleanup(func() {
		st.Wait() // want "ST001:"
	})
}

func outsideHelper() {
	st.Wait() // want "ST001:"
}

func TestInside(t *testing.T) {
	st.Test(t, func(t *testing.T) {
		t.Parallel()                           // want "ST003:"
		_, _ = t.Deadline()                    // want "ST003:"
		t.Run("nested", func(t *testing.T) {}) // want "ST003:"
		alias := t
		alias.Parallel() // want "ST003:"
		parallel := t.Parallel
		parallel() // want "ST003:"
		prohibitedHelper(t)
		st.Test(t, func(t *testing.T) {}) // want "ST002:"
		nested(t)
		go func() {
			st.Test(t, func(t *testing.T) {}) // want "ST002:"
		}()
	})
}

func prohibitedHelper(t *testing.T) {
	t.Parallel() // want "ST003:"
}

func nested(t *testing.T) {
	st.Test(t, func(t *testing.T) {}) // want "ST002:"
}

func TestNamedCallback(t *testing.T) {
	run := st.Test
	run(t, namedCallback)
}

func namedCallback(t *testing.T) {
	t.Parallel() // want "ST003:"
	st.Wait()
}

func TestLocalCallback(t *testing.T) {
	cb := func(t *testing.T) {
		t.Parallel() // want "ST003:"
	}
	st.Test(t, cb)
}

func TestSharedHelper(t *testing.T) {
	st.Test(t, func(t *testing.T) { sharedHelper() })
	sharedHelper()
}

func sharedHelper() {
	st.Wait() // want "ST001:"
}

func TestOuterCleanup(t *testing.T) {
	outer := t
	st.Test(t, func(t *testing.T) {
		outer.Cleanup(func() {
			st.Wait() // want "ST001:"
		})
	})
}
