package greet

import (
	"testing"

	"example.com/testonly-embed/internal/testutil"
)

func TestGreet(t *testing.T) {
	if Greet("world") != testutil.Expected() {
		t.Fatal("mismatch")
	}
}
