package main

import "testing"

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
		{
			name:  "literal backslash preserved",
			flags: []string{"-X main.Path=C:\\Users\\me"},
			want:  []string{"-X", "main.Path=C:\\Users\\me"},
		},
		{
			name:  "mid-token quote is literal",
			flags: []string{`a"b c"d`},
			want:  []string{`a"b`, `c"d`},
		},
		{
			name:  "adjacent quoted fields",
			flags: []string{`"hello" "world"`},
			want:  []string{"hello", "world"},
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

func TestHasExtld(t *testing.T) {
	tests := []struct {
		name    string
		ldflags []string
		want    bool
	}{
		{"absent", []string{"-s", "-w"}, false},
		{"separate form", []string{"-s", "-extld", "/custom/ld"}, true},
		{"equals form", []string{"-extld=/custom/ld"}, true},
		{"extldflags is not extld", []string{"-extldflags", "-static"}, false},
		{"extldflags equals form is not extld", []string{"-extldflags=-static"}, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasExtld(tt.ldflags); got != tt.want {
				t.Errorf("hasExtld(%v) = %v, want %v", tt.ldflags, got, tt.want)
			}
		})
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
