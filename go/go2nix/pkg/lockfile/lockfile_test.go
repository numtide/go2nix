package lockfile

import (
	"path/filepath"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	orig := &Lockfile{
		Mod: map[string]ModEntry{
			"github.com/foo/bar@v1.2.3": {Version: "v1.2.3", Hash: "sha256-aaa=", NumPkgs: 1},
		},
		Pkg: map[string]PkgEntry{},
	}
	if err := orig.Write(path, Header); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Mod["github.com/foo/bar@v1.2.3"].Hash != "sha256-aaa=" {
		t.Errorf("hash mismatch: %+v", got.Mod)
	}
}

func TestReadMissing(t *testing.T) {
	lf, err := Read(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("missing file should return empty, got err: %v", err)
	}
	if len(lf.Mod) != 0 {
		t.Errorf("expected empty lockfile, got %d entries", len(lf.Mod))
	}
}
