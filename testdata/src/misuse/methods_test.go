package misuse

import (
	"testing"
	"testing/synctest"
)

type methodHelper struct{}

func (methodHelper) waitValue() { synctest.Wait() } // want "ST001:"
func (*methodHelper) waitPointer() {
	synctest.Wait() // want "ST001:"
}
func (methodHelper) waitAlias() { synctest.Wait() } // want "ST001:"
func (methodHelper) cleanup()   { synctest.Wait() } // want "ST001:"

func TestMethodHelpersOutside(t *testing.T) {
	h := methodHelper{}
	h.waitValue()
	h.waitPointer()
	wait := h.waitAlias
	wait()
	t.Cleanup(h.cleanup)
}

func (methodHelper) expression(t *testing.T) { t.Parallel() } // want "ST003:"
func (*methodHelper) expressionAlias(t *testing.T) {
	t.Parallel() // want "ST003:"
}
func (methodHelper) bound(t *testing.T) { t.Parallel() } // want "ST003:"
func (methodHelper) callback(t *testing.T) {
	t.Parallel() // want "ST003:"
}
func (methodHelper) nested(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {}) // want "ST002:"
}

func TestMethodHelpersInBubble(t *testing.T) {
	h := methodHelper{}
	synctest.Test(t, func(bt *testing.T) {
		methodHelper.expression(h, bt)
		expression := (*methodHelper).expressionAlias
		expression(&h, bt)
		bound := h.bound
		bound(bt)
		h.nested(bt)
	})
	synctest.Test(t, h.callback)
}

type embeddedMethodHelper struct{ methodHelper }

func (methodHelper) promoted(t *testing.T) {
	t.Parallel() // want "ST003:"
}

func TestPromotedMethodExpression(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		embeddedMethodHelper.promoted(embeddedMethodHelper{}, bt)
	})
}

type genericMethodHelper[T any] struct{}

func (genericMethodHelper[T]) check(t *testing.T) {
	t.Parallel() // want "ST003:"
}

func TestGenericMethodHelper(t *testing.T) {
	synctest.Test(t, func(bt *testing.T) {
		genericMethodHelper[int]{}.check(bt)
	})
}
