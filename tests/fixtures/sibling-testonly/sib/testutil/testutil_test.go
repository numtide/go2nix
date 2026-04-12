package testutil

import (
	_ "embed"
	"testing"
)

// fixture.bin is NOT under testdata/. testutil is a test-only-local in a
// sibling-replace module: it never appears as a `go list -test` PATTERN
// when only build-graph import paths are passed, so cmd/go would leave
// TestEmbedFiles unresolved (load/test.go:132). The plugin's `<mod>/...`
// pattern expansion must make this resolve so mainSrc includes the file.
//
//go:embed fixture.bin
var fixture []byte

func TestFixtureNotEmpty(t *testing.T) {
	if len(fixture) == 0 {
		t.Fatal("embedded fixture is empty")
	}
}
