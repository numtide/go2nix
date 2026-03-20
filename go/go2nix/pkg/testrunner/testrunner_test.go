package testrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverrideImportCfgEntry(t *testing.T) {
	tests := []struct {
		name       string
		initial    string
		importPath string
		archive    string
		want       string
	}{
		{
			name:       "add new entry",
			initial:    "packagefile fmt=/nix/store/xxx/fmt.a\n",
			importPath: "example.com/foo",
			archive:    "/tmp/test/foo.a",
			want:       "packagefile fmt=/nix/store/xxx/fmt.a\npackagefile example.com/foo=/tmp/test/foo.a\n",
		},
		{
			name:       "replace existing entry",
			initial:    "packagefile example.com/foo=/old/path.a\npackagefile fmt=/nix/store/xxx/fmt.a\n",
			importPath: "example.com/foo",
			archive:    "/new/path.a",
			want:       "packagefile fmt=/nix/store/xxx/fmt.a\npackagefile example.com/foo=/new/path.a\n",
		},
		{
			name:       "no false match on prefix",
			initial:    "packagefile example.com/foobar=/keep.a\npackagefile example.com/foo=/old.a\n",
			importPath: "example.com/foo",
			archive:    "/new.a",
			want:       "packagefile example.com/foobar=/keep.a\npackagefile example.com/foo=/new.a\n",
		},
		{
			name:       "remove duplicates",
			initial:    "packagefile example.com/foo=/first.a\npackagefile example.com/foo=/second.a\n",
			importPath: "example.com/foo",
			archive:    "/override.a",
			want:       "packagefile example.com/foo=/override.a\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "importcfg")
			if err := os.WriteFile(f, []byte(tt.initial), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := overrideImportCfgEntry(f, tt.importPath, tt.archive); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"example.com/foo/bar", "example_com_foo_bar"},
		{"simple", "simple"},
		{"a.b/c.d", "a_b_c_d"},
	}
	for _, tt := range tests {
		if got := sanitize(tt.input); got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
