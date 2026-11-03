package integration_nolint

import (
	"testing"
	"testing/synctest"
)

func TestSuppressed(t *testing.T) {
	synctest.Wait() //nolint:bubblelint // This intentional misuse tests suppression.
}

func TestVisible(t *testing.T) {
	synctest.Wait() // want "ST001:"
}
