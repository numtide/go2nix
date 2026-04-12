package embed

import (
	_ "embed"
	"testing"
)

// schema_test.json is NOT under testdata/ so its presence in mainSrc
// proves the local_test_embed_files merge from the -test pass works,
// not just the testdata-dir prefix rule.
//
//go:embed schema_test.json
var golden string

func TestSchemaMatchesGolden(t *testing.T) {
	if Schema() != golden {
		t.Fatalf("schema = %q, golden = %q", Schema(), golden)
	}
}
