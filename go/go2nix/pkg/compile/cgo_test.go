package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterCppFlags(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"empty", nil, []string{}},
		{"no cpp flags", []string{"-lfoo", "-lbar"}, []string{"-lfoo", "-lbar"}},
		{"removes -lc++", []string{"-lfoo", "-lc++", "-lbar"}, []string{"-lfoo", "-lbar"}},
		{"removes -lstdc++", []string{"-lstdc++", "-lm"}, []string{"-lm"}},
		{"removes both", []string{"-lc++", "-lstdc++", "-lpthread"}, []string{"-lpthread"}},
		{"keeps similar flags", []string{"-lc++abi", "-lstdc++fs"}, []string{"-lc++abi", "-lstdc++fs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterCppFlags(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestMatchesCgoConstraint(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		goos       string
		goarch     string
		tags       []string
		want       bool
	}{
		{"os match", "linux", "linux", "amd64", nil, true},
		{"os mismatch", "darwin", "linux", "amd64", nil, false},
		{"arch match", "amd64", "linux", "amd64", nil, true},
		{"arch mismatch", "arm64", "linux", "amd64", nil, false},
		{"os,arch match", "linux,amd64", "linux", "amd64", nil, true},
		{"os,arch partial", "linux,arm64", "linux", "amd64", nil, false},
		{"negated os match", "!windows", "linux", "amd64", nil, true},
		{"negated os mismatch", "!linux", "linux", "amd64", nil, false},
		{"negated arch", "!arm64", "linux", "amd64", nil, true},
		{"combined negated", "!windows,amd64", "linux", "amd64", nil, true},
		{"empty constraint", "", "linux", "amd64", nil, true},
		// Space = OR of comma-AND groups (regression for finding #20).
		{"space OR first matches", "linux darwin", "linux", "amd64", nil, true},
		{"space OR second matches", "linux darwin", "darwin", "arm64", nil, true},
		{"space OR none match", "linux darwin", "windows", "amd64", nil, false},
		{"AND-OR mix linux", "linux,amd64 darwin", "linux", "amd64", nil, true},
		{"AND-OR mix darwin", "linux,amd64 darwin", "darwin", "arm64", nil, true},
		{"AND-OR mix linux/arm fails AND", "linux,amd64 darwin", "linux", "arm64", nil, false},
		// Well-known tags.
		{"unix on linux", "unix", "linux", "amd64", nil, true},
		{"unix on windows", "unix", "windows", "amd64", nil, false},
		{"cgo always true", "cgo", "linux", "amd64", nil, true},
		{"gc always true", "gc", "linux", "amd64", nil, true},
		// Custom build tags.
		{"custom tag set", "mytag", "linux", "amd64", []string{"mytag"}, true},
		{"custom tag unset", "mytag", "linux", "amd64", nil, false},
		{"custom tag in AND", "linux,mytag", "linux", "amd64", []string{"mytag"}, true},
		{"custom tag negated", "!mytag", "linux", "amd64", []string{"mytag"}, false},
		// Edge cases.
		{"bare bang fails", "!", "linux", "amd64", nil, false},
		{"trailing comma fails group", "linux,", "linux", "amd64", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCgoConstraint(tt.constraint, tt.goos, tt.goarch, tt.tags)
			if got != tt.want {
				t.Errorf("matchesCgoConstraint(%q, %q, %q, %v) = %v, want %v",
					tt.constraint, tt.goos, tt.goarch, tt.tags, got, tt.want)
			}
		})
	}
}

func TestCgoPreamble(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		contain string
	}{
		{
			"standalone import",
			"package foo\n\n// #cgo pkg-config: libfoo\nimport \"C\"\n",
			"#cgo pkg-config: libfoo",
		},
		{
			"grouped import",
			"package foo\n\nimport (\n// #cgo LDFLAGS: -lfoo\n\"C\"\n)\n",
			"#cgo LDFLAGS: -lfoo",
		},
		{
			"block comment",
			"package foo\n\n/*\n#cgo pkg-config: bar\n#include <bar.h>\n*/\nimport \"C\"\n",
			"#cgo pkg-config: bar",
		},
		{
			"no import C",
			"package foo\n\nimport \"fmt\"\n",
			"",
		},
		{
			"garbage file",
			"this is not valid go code at all {{{",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.go")
			if err := os.WriteFile(path, []byte(tt.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got := cgoPreamble(path)
			if tt.contain == "" {
				if got != "" {
					t.Errorf("expected empty preamble, got %q", got)
				}
			} else if !strings.Contains(got, tt.contain) {
				t.Errorf("preamble %q does not contain %q", got, tt.contain)
			}
		})
	}
}
