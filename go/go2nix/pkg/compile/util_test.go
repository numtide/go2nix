package compile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvOrDefault(t *testing.T) {
	key := "GO2NIX_TEST_ENV_OR_DEFAULT"

	os.Unsetenv(key)
	if got := envOrDefault(key, "fallback"); got != "fallback" {
		t.Errorf("unset: got %q, want %q", got, "fallback")
	}

	t.Setenv(key, "override")
	if got := envOrDefault(key, "fallback"); got != "override" {
		t.Errorf("set: got %q, want %q", got, "override")
	}
}

func TestExtraGCFlags(t *testing.T) {
	if got := extraGCFlags(Options{GCFlags: ""}); got != nil {
		t.Errorf("empty: got %v, want nil", got)
	}
	got := extraGCFlags(Options{GCFlags: "-race -N -l"})
	want := []string{"-race", "-N", "-l"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDefaultBuildMode(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "exe"},
		{"linux", "arm64", "exe"},
		{"darwin", "amd64", "pie"},
		{"darwin", "arm64", "pie"},
		{"windows", "amd64", "pie"},
		{"android", "arm64", "pie"},
		{"ios", "arm64", "pie"},
		{"freebsd", "amd64", "exe"},
	}
	for _, tt := range tests {
		got := DefaultBuildMode(tt.goos, tt.goarch)
		if got != tt.want {
			t.Errorf("DefaultBuildMode(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestLangVersion(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1.21", "1.21"},
		{"1.21.3", "1.21"},
		{"1.22.0", "1.22"},
		{"1", "1"},
		{"2.0", "2.0"},
	}
	for _, tt := range tests {
		if got := LangVersion(tt.in); got != tt.want {
			t.Errorf("LangVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFindGoVersion(t *testing.T) {
	// Create a module root with go.mod.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\n\ngo 1.21.3\n"), 0o644)

	// From the module root itself.
	if got := findGoVersion(dir); got != "1.21" {
		t.Errorf("root: got %q, want %q", got, "1.21")
	}

	// From a sub-package directory.
	subdir := filepath.Join(dir, "pkg", "bar")
	os.MkdirAll(subdir, 0o755)
	if got := findGoVersion(subdir); got != "1.21" {
		t.Errorf("subdir: got %q, want %q", got, "1.21")
	}

	// Directory with no go.mod returns "".
	nomod := t.TempDir()
	if got := findGoVersion(nomod); got != "" {
		t.Errorf("no go.mod: got %q, want %q", got, "")
	}
}

func TestExtractPackageName(t *testing.T) {
	dir := t.TempDir()

	main := filepath.Join(dir, "main.go")
	os.WriteFile(main, []byte("package main\n"), 0o644)
	if got := extractPackageName(main); got != "main" {
		t.Errorf("main.go: got %q, want %q", got, "main")
	}

	lib := filepath.Join(dir, "lib.go")
	os.WriteFile(lib, []byte("package mylib\n"), 0o644)
	if got := extractPackageName(lib); got != "mylib" {
		t.Errorf("lib.go: got %q, want %q", got, "mylib")
	}

	// Non-existent file falls back to "main".
	if got := extractPackageName(filepath.Join(dir, "nope.go")); got != "main" {
		t.Errorf("missing: got %q, want %q", got, "main")
	}

	// Invalid syntax falls back to "main".
	bad := filepath.Join(dir, "bad.go")
	os.WriteFile(bad, []byte("not valid go {{{"), 0o644)
	if got := extractPackageName(bad); got != "main" {
		t.Errorf("invalid syntax: got %q, want %q", got, "main")
	}
}
