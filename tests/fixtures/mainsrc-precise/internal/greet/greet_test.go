package greet

import (
	"os"
	"strings"
	"testing"
)

func TestHello(t *testing.T) {
	want, err := os.ReadFile("testdata/expected.txt")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if got := Hello(); got != strings.TrimSpace(string(want)) {
		t.Fatalf("Hello() = %q, want %q", got, strings.TrimSpace(string(want)))
	}
}
