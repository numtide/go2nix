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

// adjacent.conf is NOT under testdata/ and NOT //go:embed-ed; it reaches
// mainSrc only because dag.nix lists it in extraMainSrcFiles.
func TestReadAdjacent(t *testing.T) {
	b, err := os.ReadFile("adjacent.conf")
	if err != nil {
		t.Fatalf("read adjacent.conf: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != "adjacent-value" {
		t.Fatalf("adjacent.conf = %q, want %q", got, "adjacent-value")
	}
}
