package testrunner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/numtide/go2nix/pkg/gofiles"
	"github.com/numtide/go2nix/pkg/localpkgs"
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

func TestInternalTestPkgFiles(t *testing.T) {
	// Regression for finding #3: the internal-test compile must carry the
	// package's CgoFiles/SFiles/SysoFiles/etc. through so CompileGoPackage
	// routes to the cgo/asm paths instead of the pure-Go -complete path.
	pkg := &localpkgs.LocalPkg{
		TestGoFiles: []string{"a_test.go"},
		PkgFiles: gofiles.PkgFiles{
			GoFiles:   []string{"a.go", "b.go"},
			CgoFiles:  []string{"cgo.go"},
			SFiles:    []string{"asm_amd64.s"},
			CFiles:    []string{"impl.c"},
			CXXFiles:  []string{"impl.cc"},
			HFiles:    []string{"impl.h"},
			FFiles:    []string{"impl.f90"},
			SysoFiles: []string{"blob.syso"},
		},
	}
	got := internalTestPkgFiles(pkg, nil)

	wantGo := []string{"a.go", "b.go", "a_test.go"}
	if !reflect.DeepEqual(got.GoFiles, wantGo) {
		t.Errorf("GoFiles = %v, want %v", got.GoFiles, wantGo)
	}
	if !reflect.DeepEqual(got.CgoFiles, []string{"cgo.go"}) {
		t.Errorf("CgoFiles = %v, want [cgo.go]", got.CgoFiles)
	}
	if !reflect.DeepEqual(got.SFiles, []string{"asm_amd64.s"}) {
		t.Errorf("SFiles = %v, want [asm_amd64.s]", got.SFiles)
	}
	if !reflect.DeepEqual(got.CFiles, []string{"impl.c"}) {
		t.Errorf("CFiles = %v, want [impl.c]", got.CFiles)
	}
	if !reflect.DeepEqual(got.CXXFiles, []string{"impl.cc"}) {
		t.Errorf("CXXFiles = %v, want [impl.cc]", got.CXXFiles)
	}
	if !reflect.DeepEqual(got.HFiles, []string{"impl.h"}) {
		t.Errorf("HFiles = %v, want [impl.h]", got.HFiles)
	}
	if !reflect.DeepEqual(got.FFiles, []string{"impl.f90"}) {
		t.Errorf("FFiles = %v, want [impl.f90]", got.FFiles)
	}
	if !reflect.DeepEqual(got.SysoFiles, []string{"blob.syso"}) {
		t.Errorf("SysoFiles = %v, want [blob.syso]", got.SysoFiles)
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
