package embed

import (
	_ "embed"
	"testing"
)

//go:embed testdata/golden.json
var golden string

func TestSchemaMatchesGolden(t *testing.T) {
	if Schema() != golden {
		t.Fatalf("schema = %q, golden = %q", Schema(), golden)
	}
}
