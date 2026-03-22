package testutil

import "testing"

// AssertOK fails the test if s is not "ok".
func AssertOK(t *testing.T, s string) {
	t.Helper()
	if s != "ok" {
		t.Fatalf("got %q, want ok", s)
	}
}
