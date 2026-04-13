package lockfilegen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/numtide/go2nix/pkg/golist"
)

func TestIsLocal(t *testing.T) {
	tests := []struct {
		name string
		mod  *golist.Module
		want bool
	}{
		{"nil module", nil, true},
		{"main module", &golist.Module{Path: "example.com/app", Main: true}, true},
		{"local replace (no version)", &golist.Module{
			Path:    "example.com/lib",
			Version: "v1.0.0",
			Replace: &golist.Replace{Path: "../lib"},
		}, true},
		{"remote replace (has version)", &golist.Module{
			Path:    "example.com/lib",
			Version: "v1.0.0",
			Replace: &golist.Replace{Path: "example.com/lib-fork", Version: "v2.0.0"},
		}, false},
		{"regular third-party", &golist.Module{
			Path:    "github.com/foo/bar",
			Version: "v1.2.3",
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.IsLocal(); got != tt.want {
				t.Errorf("IsLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReplaced(t *testing.T) {
	tests := []struct {
		name string
		mod  golist.ModInfo
		want string
	}{
		{
			"no replacement",
			golist.ModInfo{Key: "github.com/foo/bar@v1.0.0", FetchPath: "github.com/foo/bar", Version: "v1.0.0"},
			"",
		},
		{
			"replaced with different path",
			golist.ModInfo{Key: "github.com/foo/bar@v1.0.0", FetchPath: "github.com/foo/bar-fork", Version: "v1.0.0"},
			"github.com/foo/bar-fork",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.Replaced(); got != tt.want {
				t.Errorf("Replaced() = %q, want %q", got, tt.want)
			}
		})
	}
}

// hashModuleSource must hash only the extracted source tree, so the result
// is independent of cache/download/*.info (which carries a proxy-specific
// Origin block under GOPROXY=direct) and cache/vcs/ (bare git clones).
func TestHashModuleSource_ProxyIndependent(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "example.com", "!foo", "bar@v1.0.0")
	mustMkdirAll(t, src)
	mustWrite(t, filepath.Join(src, "a.go"), "package bar\n")

	dl := filepath.Join(tmp, "cache", "download", "example.com", "!foo", "bar", "@v")
	mustMkdirAll(t, dl)
	mustWrite(t, filepath.Join(dl, "v1.0.0.info"), `{"Version":"v1.0.0"}`)

	h1, err := hashModuleSource(tmp, "example.com/Foo/bar", "v1.0.0")
	if err != nil {
		t.Fatalf("hashModuleSource: %v", err)
	}

	mustWrite(t, filepath.Join(dl, "v1.0.0.info"),
		`{"Version":"v1.0.0","Origin":{"VCS":"git","URL":"https://example.com/Foo/bar","Ref":"refs/tags/v1.0.0","Hash":"abc"}}`)
	vcs := filepath.Join(tmp, "cache", "vcs", "deadbeef")
	mustMkdirAll(t, vcs)
	mustWrite(t, filepath.Join(vcs, "HEAD"), "ref: refs/heads/main\n")

	h2, err := hashModuleSource(tmp, "example.com/Foo/bar", "v1.0.0")
	if err != nil {
		t.Fatalf("hashModuleSource: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash changed after mutating cache/ only:\n  before: %s\n  after:  %s", h1, h2)
	}

	mustWrite(t, filepath.Join(src, "a.go"), "package bar // changed\n")
	h3, err := hashModuleSource(tmp, "example.com/Foo/bar", "v1.0.0")
	if err != nil {
		t.Fatalf("hashModuleSource: %v", err)
	}
	if h1 == h3 {
		t.Errorf("hash unchanged after mutating source tree (should differ)")
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
