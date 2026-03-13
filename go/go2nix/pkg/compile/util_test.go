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
