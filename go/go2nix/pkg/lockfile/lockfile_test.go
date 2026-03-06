package lockfile

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRoundtripAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	orig := &Lockfile{
		Mod: map[string]ModEntry{
			"github.com/foo/bar@v1.2.3": {
				Version:  "v1.2.3",
				Hash:     "sha256-aaa=",
				Replaced: "github.com/foo/bar-fork",
				NumPkgs:  2,
			},
		},
		Pkg: map[string]PkgEntry{
			"github.com/foo/bar": {
				Module:  "github.com/foo/bar@v1.2.3",
				Imports: []string{"github.com/baz/qux", "github.com/x/y"},
			},
		},
	}
	if err := orig.Write(path, Header); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	mod := got.Mod["github.com/foo/bar@v1.2.3"]
	if mod.Replaced != "github.com/foo/bar-fork" {
		t.Errorf("replaced: got %q, want %q", mod.Replaced, "github.com/foo/bar-fork")
	}
	if mod.NumPkgs != 2 {
		t.Errorf("num_pkgs: got %d, want 2", mod.NumPkgs)
	}

	pkg := got.Pkg["github.com/foo/bar"]
	if pkg.Module != "github.com/foo/bar@v1.2.3" {
		t.Errorf("module: got %q, want %q", pkg.Module, "github.com/foo/bar@v1.2.3")
	}
	if len(pkg.Imports) != 2 || pkg.Imports[0] != "github.com/baz/qux" || pkg.Imports[1] != "github.com/x/y" {
		t.Errorf("imports: got %v, want [github.com/baz/qux github.com/x/y]", pkg.Imports)
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

func TestReadUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	data := `
[mod."github.com/foo/bar@v1.0.0"]
  version = "v1.0.0"
  hash = "sha256-aaa="
  num_pkgs = 1
  typo_field = "oops"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Read(path)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("expected 'unknown keys' in error, got: %v", err)
	}
}

func TestReadMalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	if err := os.WriteFile(path, []byte("[invalid toml\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Read(path)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}
