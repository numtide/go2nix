package gofiles

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestListFilesRespectsTargetGoVersion is the regression test for the bug
// where gofiles.BuildContext used build.Default.ReleaseTags (the Go version
// that built the go2nix binary) instead of the target toolchain's version,
// causing //go:build go1.N constraints to be evaluated against the wrong
// release tags.
//
// Repro from the bug report: a package with two version-gated files. When
// asked to list files for go1.25, only the !go1.26 file must be selected,
// regardless of which Go version built this test binary.
func TestListFilesRespectsTargetGoVersion(t *testing.T) {
	dir := t.TempDir()

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("base.go", "package p\n\nfunc Base() int { return 1 }\n")
	write("config_go125.go", "//go:build !go1.26\n\npackage p\n\nfunc Versioned() int { return 125 }\n")
	write("config_go126.go", "//go:build go1.26\n\npackage p\n\nfunc Versioned() int { return 126 }\n")

	// Target Go 1.25: must select base.go + config_go125.go, never config_go126.go.
	files, err := ListFiles(dir, "", "1.25")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}

	want := []string{"base.go", "config_go125.go"}
	got := append([]string(nil), files.GoFiles...)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("GoFiles for go1.25 target = %v, want %v", got, want)
	}

	// Target Go 1.26: should now flip to selecting config_go126.go.
	files, err = ListFiles(dir, "", "1.26")
	if err != nil {
		t.Fatalf("ListFiles go1.26: %v", err)
	}
	want = []string{"base.go", "config_go126.go"}
	got = append([]string(nil), files.GoFiles...)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("GoFiles for go1.26 target = %v, want %v", got, want)
	}
}

func TestReleaseTagsForVersion(t *testing.T) {
	cases := []struct {
		in      string
		wantLen int // number of "go1.N" tags
		last    string
	}{
		{"1.25", 25, "go1.25"},
		{"1.25.3", 25, "go1.25"},
		{"go1.25", 25, "go1.25"},
		{"go1.25.3", 25, "go1.25"},
		{"1.1", 1, "go1.1"},
		// Prerelease toolchains: `go env GOVERSION` returns e.g.
		// "go1.26rc1" with no dot before the suffix.
		{"go1.26rc1", 26, "go1.26"},
		{"go1.26beta2", 26, "go1.26"},
		{"1.26rc1", 26, "go1.26"},
	}
	for _, tc := range cases {
		got := ReleaseTagsForVersion(tc.in)
		if len(got) != tc.wantLen {
			t.Errorf("ReleaseTagsForVersion(%q) len = %d, want %d (%v)", tc.in, len(got), tc.wantLen, got)
		}
		if len(got) > 0 && got[len(got)-1] != tc.last {
			t.Errorf("ReleaseTagsForVersion(%q) last = %q, want %q", tc.in, got[len(got)-1], tc.last)
		}
	}

	// Empty input means "do not override" — return nil so callers fall
	// back to build.Default.ReleaseTags.
	if got := ReleaseTagsForVersion(""); got != nil {
		t.Errorf("ReleaseTagsForVersion(\"\") = %v, want nil", got)
	}
}
