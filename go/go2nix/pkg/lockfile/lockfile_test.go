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

func TestMinimalLockfileOmitsPkg(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	// Minimal lockfile: Pkg is nil (not empty map)
	orig := &Lockfile{
		Mod: map[string]string{
			"github.com/foo/bar@v1.0.0": "sha256-aaa=",
		},
	}
	if err := orig.Write(path, Header); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[pkg]") {
		t.Error("nil Pkg should be omitted from output")
	}

	// Should still be readable
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Mod["github.com/foo/bar@v1.0.0"] != "sha256-aaa=" {
		t.Errorf("hash mismatch: %+v", got.Mod)
	}
}

func TestToGomod2nix(t *testing.T) {
	v2 := &Lockfile{
		Mod: map[string]string{
			"github.com/foo/bar@v1.2.3": "sha256-aaa=",
			"github.com/baz/qux@v0.1.0": "sha256-bbb=",
		},
		Replace: map[string]string{
			"github.com/foo/bar@v1.2.3": "github.com/foo/bar-fork",
		},
	}

	got := v2.ToGomod2nix()

	if len(got.Mod) != 2 {
		t.Fatalf("expected 2 mods, got %d", len(got.Mod))
	}

	foo := got.Mod["github.com/foo/bar@v1.2.3"]
	if foo.Version != "v1.2.3" {
		t.Errorf("foo version = %q, want v1.2.3", foo.Version)
	}
	if foo.Hash != "sha256-aaa=" {
		t.Errorf("foo hash = %q, want sha256-aaa=", foo.Hash)
	}
	if foo.Replaced != "github.com/foo/bar-fork" {
		t.Errorf("foo replaced = %q, want github.com/foo/bar-fork", foo.Replaced)
	}

	baz := got.Mod["github.com/baz/qux@v0.1.0"]
	if baz.Version != "v0.1.0" {
		t.Errorf("baz version = %q, want v0.1.0", baz.Version)
	}
	if baz.Replaced != "" {
		t.Errorf("baz replaced = %q, want empty", baz.Replaced)
	}
}

func TestGomod2nixRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	orig := &Gomod2nixLockfile{
		Mod: map[string]Gomod2nixMod{
			"github.com/foo/bar@v1.2.3": {
				Version:  "v1.2.3",
				Hash:     "sha256-aaa=",
				Replaced: "github.com/foo/bar-fork",
			},
		},
	}
	if err := orig.Write(path, Gomod2nixHeader); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should contain the gomod2nix header.
	if !strings.Contains(content, "gomod2nix format") {
		t.Error("expected gomod2nix header")
	}

	// Should have attrset-style mod entries (version/hash fields).
	if !strings.Contains(content, "version =") || !strings.Contains(content, "hash =") {
		t.Error("expected version/hash fields in mod entries")
	}

	// Should not have [pkg] or [replace] sections.
	if strings.Contains(content, "[pkg") {
		t.Error("gomod2nix lockfile should not contain [pkg]")
	}
	if strings.Contains(content, "[replace]") {
		t.Error("gomod2nix lockfile should not contain [replace] (replaced is per-mod)")
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
