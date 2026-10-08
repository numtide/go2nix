package a_test

import (
	"testing"

	"example.com/toolchain-line/internal/b"
)

func TestViaB(t *testing.T) {
	if got := b.Greeting(); got != "ok" {
		t.Fatalf("b.Greeting() = %q, want %q", got, "ok")
	}
}
