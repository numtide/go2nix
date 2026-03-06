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
		want       bool
	}{
		{"os match", "linux", "linux", "amd64", true},
		{"os mismatch", "darwin", "linux", "amd64", false},
		{"arch match", "amd64", "linux", "amd64", true},
		{"arch mismatch", "arm64", "linux", "amd64", false},
		{"os,arch match", "linux,amd64", "linux", "amd64", true},
		{"os,arch partial", "linux,arm64", "linux", "amd64", false},
		{"negated os match", "!windows", "linux", "amd64", true},
		{"negated os mismatch", "!linux", "linux", "amd64", false},
		{"negated arch", "!arm64", "linux", "amd64", true},
		{"combined negated", "!windows,amd64", "linux", "amd64", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCgoConstraint(tt.constraint, tt.goos, tt.goarch)
			if got != tt.want {
				t.Errorf("matchesCgoConstraint(%q, %q, %q) = %v, want %v",
					tt.constraint, tt.goos, tt.goarch, got, tt.want)
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
