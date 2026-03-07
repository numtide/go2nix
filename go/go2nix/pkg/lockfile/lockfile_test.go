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
		Mod: map[string]string{
			"github.com/foo/bar@v1.2.3": "sha256-aaa=",
		},
		Pkg: map[string]map[string][]string{},
	}
	if err := orig.Write(path, Header); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Mod["github.com/foo/bar@v1.2.3"] != "sha256-aaa=" {
		t.Errorf("hash mismatch: %+v", got.Mod)
	}
}

func TestRoundtripAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	orig := &Lockfile{
		Mod: map[string]string{
			"github.com/foo/bar@v1.2.3": "sha256-aaa=",
		},
		Replace: map[string]string{
			"github.com/foo/bar@v1.2.3": "github.com/foo/bar-fork",
		},
		Pkg: map[string]map[string][]string{
			"github.com/foo/bar@v1.2.3": {
				"github.com/foo/bar": {"github.com/baz/qux", "github.com/x/y"},
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

	if got.Replace["github.com/foo/bar@v1.2.3"] != "github.com/foo/bar-fork" {
		t.Errorf("replace: got %q, want %q", got.Replace["github.com/foo/bar@v1.2.3"], "github.com/foo/bar-fork")
	}

	pkgMap := got.Pkg["github.com/foo/bar@v1.2.3"]
	if pkgMap == nil {
		t.Fatal("expected pkg group for github.com/foo/bar@v1.2.3")
	}
	imports := pkgMap["github.com/foo/bar"]
	if len(imports) != 2 || imports[0] != "github.com/baz/qux" || imports[1] != "github.com/x/y" {
		t.Errorf("imports: got %v, want [github.com/baz/qux github.com/x/y]", imports)
	}
}

func TestRoundtripOmitReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	orig := &Lockfile{
		Mod: map[string]string{
			"github.com/foo/bar@v1.0.0": "sha256-aaa=",
		},
		Pkg: map[string]map[string][]string{},
	}
	if err := orig.Write(path, Header); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[replace]") {
		t.Error("empty replace should be omitted from output")
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
[mod]
"github.com/foo/bar@v1.0.0" = "sha256-aaa="

[bogus]
key = "oops"
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
