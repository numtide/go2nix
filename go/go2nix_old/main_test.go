package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slices"
)

func TestLockfileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.toml")

	orig := &lockfile{
		Mod: map[string]ModEntry{
			"github.com/foo/bar@v1.2.3":  {Version: "v1.2.3", Hash: "sha256-aaa=", NumPkgs: 1},
			"github.com/alpha/z@v0.0.1":  {Version: "v0.0.1", Hash: "sha256-bbb=", NumPkgs: 2},
			"github.com/foo/bar@v1.2.4":  {Version: "v1.2.4", Hash: "sha256-ccc=", NumPkgs: 1},
			"github.com/replaced@v2.0.0": {Version: "v2.0.0", Hash: "sha256-ddd=", Replaced: "github.com/fork", NumPkgs: 3},
		},
		Pkg: map[string]PkgEntry{},
	}
	if err := orig.write(path, "# header\n\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := readLockfile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Mod) != len(orig.Mod) {
		t.Fatalf("got %d entries, want %d", len(got.Mod), len(orig.Mod))
	}
	for k, v := range orig.Mod {
		if got.Mod[k] != v {
			t.Errorf("entry %q: got %+v, want %+v", k, got.Mod[k], v)
		}
	}

	// Output must be deterministic and sorted so concurrent edits to distinct
	// entries produce clean diffs.
	data, _ := os.ReadFile(path) //nolint:gosec // t.TempDir path
	s := string(data)
	keys := []string{"alpha/z@v0.0.1", "foo/bar@v1.2.3", "foo/bar@v1.2.4", "replaced@v2.0.0"}
	var positions []int
	for _, k := range keys {
		i := strings.Index(s, k)
		if i < 0 {
			t.Fatalf("key %q not found in output", k)
		}
		positions = append(positions, i)
	}
	if !slices.IsSorted(positions) {
		t.Errorf("keys not in sorted order in output; positions: %v\n%s", positions, s)
	}

	// Writing twice must produce identical bytes.
	if err := orig.write(path, "# header\n\n"); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	data2, _ := os.ReadFile(path) //nolint:gosec // t.TempDir path
	if string(data) != string(data2) {
		t.Errorf("output not deterministic:\n---first---\n%s\n---second---\n%s", data, data2)
	}
}

func TestReadLockfileMissing(t *testing.T) {
	lf, err := readLockfile(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("missing file should return empty, got err: %v", err)
	}
	if len(lf.Mod) != 0 {
		t.Errorf("missing file should return empty lockfile, got %d entries", len(lf.Mod))
	}
}

// The Nix builder reconstructs bare module paths from lockfile entries via
// removeSuffix "@${version}" — verify that contract for corner cases.
func TestEntryPathExtraction(t *testing.T) {
	cases := []struct {
		key, version, wantPath string
	}{
		{"github.com/foo/bar@v1.2.3", "v1.2.3", "github.com/foo/bar"},
		{"golang.org/x/mod@v0.32.0", "v0.32.0", "golang.org/x/mod"},
		// Pseudo-version
		{"github.com/foo/bar@v0.0.0-20240101000000-abcdef012345", "v0.0.0-20240101000000-abcdef012345", "github.com/foo/bar"},
		// Version-in-path (Go's /vN convention)
		{"github.com/foo/bar/v2@v2.1.0", "v2.1.0", "github.com/foo/bar/v2"},
	}
	for _, tc := range cases {
		got := strings.TrimSuffix(tc.key, "@"+tc.version)
		if got != tc.wantPath {
			t.Errorf("key %q version %q: got %q, want %q", tc.key, tc.version, got, tc.wantPath)
		}
	}
}
