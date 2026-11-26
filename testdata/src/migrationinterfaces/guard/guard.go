package guard

import "net"

type Reporter interface{ Fail() }
type helper interface{ Helper() }

type Assertions struct{ Reporter Reporter }

func New(t Reporter) *Assertions { return &Assertions{Reporter: t} }

func (a *Assertions) Check(value bool) {
	if h, ok := a.Reporter.(helper); ok {
		h.Helper()
	}
	Check(a.Reporter, value)
}

func Check(t Reporter, value bool) {
	if h, ok := t.(helper); ok {
		h.Helper()
	}
	if value {
		return
	}
	t.Fail()
}

func Private(value any) {
	if h, ok := value.(interface{ hidden() }); ok {
		h.hidden()
	}
}

func Signature(value any) {
	if h, ok := value.(interface{ Helper(int) }); ok {
		h.Helper(1)
	}
}

func StructSignature(value any) {
	if h, ok := value.(interface{ Helper(struct{ x int }) }); ok {
		h.Helper(struct{ x int }{})
	}
}

func CheckNil(value Reporter) {
	if value != nil {
		value.Fail()
	}
}

func Print(t interface{ Log(...any) }, value any) { t.Log(value) }

func CheckState(t Reporter) {
	if h, ok := t.(helper); ok {
		h.Helper()
	}
	if state, ok := t.(interface{ Remote() bool }); ok {
		if state.Remote() {
			t.Fail()
		}
	}
}

type Marker struct{}

func AssertedNil(value any, fail func()) {
	if pointer, ok := value.(*Marker); ok {
		if pointer == nil {
			fail()
		}
	}
}

func Equal(left, right any, otherwise func()) {
	if left == right {
		return
	}
	otherwise()
}

type Flag struct{ Enabled bool }

func EqualAddress(left, right *Flag, otherwise func()) {
	if &left.Enabled == &right.Enabled {
		return
	}
	otherwise()
}

func GenericNil[T ~*Marker](pointer T) {
	if pointer == nil {
		_, _ = net.Dial("tcp", "127.0.0.1:1")
	}
}

func GenericEqual[T comparable](left, right T, otherwise func()) {
	if left == right {
		return
	}
	otherwise()
}
