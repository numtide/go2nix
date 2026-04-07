package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandLDFlags(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		want    []string
		wantErr bool
	}{
		{
			name:  "nil input",
			flags: nil,
			want:  nil,
		},
		{
			name:  "empty list",
			flags: []string{},
			want:  nil,
		},
		{
			name:  "single-token flags pass through",
			flags: []string{"-s", "-w"},
			want:  []string{"-s", "-w"},
		},
		{
			name:  "multi-token flag is split",
			flags: []string{"-X main.Version=1.6"},
			want:  []string{"-X", "main.Version=1.6"},
		},
		{
			name:  "equals form is not split",
			flags: []string{"-X=main.Version=1.6"},
			want:  []string{"-X=main.Version=1.6"},
		},
		{
			name:  "mixed flags",
			flags: []string{"-s", "-X main.Version=1.6", "-w", "-X main.Commit=abc"},
			want:  []string{"-s", "-X", "main.Version=1.6", "-w", "-X", "main.Commit=abc"},
		},
		{
			name:  "quoted extldflags are preserved as one value",
			flags: []string{"-extldflags '-static -L/foo/lib '"},
			want:  []string{"-extldflags", "-static -L/foo/lib "},
		},
		{
			name:  "double-quoted values are preserved",
			flags: []string{`-X "main.Message=hello world"`},
			want:  []string{"-X", "main.Message=hello world"},
		},
		{
			name:  "extra whitespace is collapsed",
			flags: []string{"  -s  ", "  -X   main.Version=1.6  "},
			want:  []string{"-s", "-X", "main.Version=1.6"},
		},
		{
			name:  "empty string element is skipped",
			flags: []string{"", "-s", ""},
			want:  []string{"-s"},
		},
		{
			name:    "unterminated quoted string returns error",
			flags:   []string{"-extldflags '-static -L/foo/lib"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandLDFlags(tt.flags)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expandLDFlags(%v) returned nil error, want error", tt.flags)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandLDFlags(%v) returned unexpected error: %v", tt.flags, err)
			}
			if !slicesEqual(got, tt.want) {
				t.Errorf("expandLDFlags(%v) = %v, want %v", tt.flags, got, tt.want)
			}
		})
	}
}

func TestExtractModulePath(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")

	if err := os.WriteFile(gomod, []byte("module example.com/myapp\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := extractModulePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.com/myapp" {
		t.Errorf("got %q, want %q", got, "example.com/myapp")
	}

	// Missing go.mod returns error.
	_, err = extractModulePath(t.TempDir())
	if err == nil {
		t.Error("expected error for missing go.mod")
	}

	// go.mod without module directive returns error.
	nomod := filepath.Join(t.TempDir(), "go.mod")
	if err = os.WriteFile(nomod, []byte("go 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = extractModulePath(filepath.Dir(nomod))
	if err == nil {
		t.Error("expected error for go.mod without module directive")
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
