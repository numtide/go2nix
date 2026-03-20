package nixdrv

import (
	"testing"

	"github.com/numtide/go2nix/internal/gonix/storepath"
)

// Test vectors from nix-ninja (crates/nix-libstore/src/placeholder.rs).

func TestStandardOutput(t *testing.T) {
	p := StandardOutput("out")
	want := "/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9"
	if got := p.Render(); got != want {
		t.Errorf("StandardOutput(\"out\").Render() = %q, want %q", got, want)
	}
}

func TestCAOutput(t *testing.T) {
	drvPath, err := storepath.FromAbsolutePath("/nix/store/g1w7hy3qg1w7hy3qg1w7hy3qg1w7hy3q-foo.drv")
	if err != nil {
		t.Fatal(err)
	}
	p, err := CAOutput(drvPath, "out")
	if err != nil {
		t.Fatal(err)
	}
	want := "/0c6rn30q4frawknapgwq386zq358m8r6msvywcvc89n6m5p2dgbz"
	if got := p.Render(); got != want {
		t.Errorf("CAOutput(foo.drv, \"out\").Render() = %q, want %q", got, want)
	}
}

func TestDynamicOutput(t *testing.T) {
	// First create a CA placeholder for foo.drv.drv (note the double .drv)
	drvPath, err := storepath.FromAbsolutePath("/nix/store/g1w7hy3qg1w7hy3qg1w7hy3qg1w7hy3q-foo.drv.drv")
	if err != nil {
		t.Fatal(err)
	}
	caPlaceholder, err := CAOutput(drvPath, "out")
	if err != nil {
		t.Fatal(err)
	}

	// Then create a dynamic placeholder from it
	dynPlaceholder := DynamicOutput(caPlaceholder, "out")
	want := "/0gn6agqxjyyalf0dpihgyf49xq5hqxgw100f0wydnj6yqrhqsb3w"
	if got := dynPlaceholder.Render(); got != want {
		t.Errorf("DynamicOutput(CA(foo.drv.drv), \"out\").Render() = %q, want %q", got, want)
	}
}

func TestOutputPathName(t *testing.T) {
	if got := OutputPathName("hello", "out"); got != "hello" {
		t.Errorf("OutputPathName(\"hello\", \"out\") = %q, want %q", got, "hello")
	}
	if got := OutputPathName("hello", "dev"); got != "hello-dev" {
		t.Errorf("OutputPathName(\"hello\", \"dev\") = %q, want %q", got, "hello-dev")
	}
}
