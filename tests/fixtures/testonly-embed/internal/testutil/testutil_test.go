package testutil

import (
	_ "embed"
	"testing"
)

// Regression guard: cmd/go only resolves TestEmbedFiles for PATTERN args
// (load/test.go:132 inside TestPackagesAndErrors). testutil is reached via
// -deps when only build-graph import paths are passed, so its
// TestEmbedFiles would be empty and fixture.json would be dropped from the
// precise mainSrc — failing this test under doCheck. The plugin's `./...`
// pattern arg makes testutil a pattern match so the embed resolves.
//
//go:embed fixture.json
var fixture string

func TestFixture(t *testing.T) {
	if fixture != "{\"ok\":true}\n" {
		t.Fatalf("unexpected: %q", fixture)
	}
}
