package adder

import (
	_ "embed"
	"strconv"
	"strings"
	"testing"
)

//go:embed testdata/expected.txt
var expected string

func TestAdd(t *testing.T) {
	want, err := strconv.Atoi(strings.TrimSpace(expected))
	if err != nil {
		t.Fatalf("parsing embedded testdata/expected.txt: %v", err)
	}
	if got := Add(2, 3); got != want {
		t.Fatalf("Add(2,3) = %d, want %d", got, want)
	}
}

// TestBannerFromOverlay is the testrunner srcOverlay regression: data.txt in
// the source tree says "hello-from-embed", but dag.nix's
// packageOverrides.<adder>.srcOverlay supplies "hello-from-overlay". The
// testrunner now layers the same overlay (via testManifest.srcOverlays)
// before ResolveEmbeds/compile, so checkPhase sees what the build saw.
func TestBannerFromOverlay(t *testing.T) {
	if got := Banner(); got != "hello-from-overlay" {
		t.Fatalf("Banner() = %q, want %q (testrunner must apply srcOverlay)", got, "hello-from-overlay")
	}
}
