package compile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/numtide/go2nix/pkg/gofiles"
)

func TestBaseCompileArgs(t *testing.T) {
	args := baseCompileArgs(Options{
		ImportCfg:   "/tmp/importcfg",
		PFlag:       "example.com/p",
		trimRewrite: "/nix/store/abc=>",
	})
	want := []string{
		"tool", "compile",
		"-importcfg", "/tmp/importcfg",
		"-p", "example.com/p",
		"-buildid", "",
		"-trimpath=/nix/store/abc=>",
		"-nolocalimports",
		"-pack",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("baseCompileArgs() = %v, want %v", args, want)
	}
}

func TestOptions_rewriteDir(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "main module (no version)",
			opts: Options{ImportPath: "example.com/m/cmd/app", ModulePath: "example.com/m"},
			want: "example.com/m/cmd/app",
		},
		{
			name: "sibling module",
			opts: Options{ImportPath: "example.com/sib/util", ModulePath: "example.com/sib", ModuleVersion: "v0.1.0"},
			want: "example.com/sib@v0.1.0/util",
		},
		{
			name: "third-party module root",
			opts: Options{ImportPath: "github.com/foo/bar", ModulePath: "github.com/foo/bar", ModuleVersion: "v1.2.3"},
			want: "github.com/foo/bar@v1.2.3",
		},
		{
			name: "no module info",
			opts: Options{ImportPath: "example.com/p"},
			want: "example.com/p",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.rewriteDir(); got != tt.want {
				t.Errorf("rewriteDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOptions_outputFlags(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "default mode (no IfaceOutput)",
			opts: Options{Output: "/out/foo.a"},
			want: []string{"-o", "/out/foo.a"},
		},
		{
			name: "iface split mode",
			opts: Options{Output: "/out/foo.a", IfaceOutput: "/iface/foo.x"},
			want: []string{"-o", "/iface/foo.x", "-linkobj", "/out/foo.a"},
		},
		{
			name: "empty IfaceOutput stays in default mode",
			opts: Options{Output: "/out/foo.a", IfaceOutput: ""},
			want: []string{"-o", "/out/foo.a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.opts.outputFlags()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("outputFlags() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Regression for finding #22: Objective-C and SWIG sources must not be
// silently dropped. Until full support lands, CompileGoPackage must fail
// fast with a clear error rather than producing an archive missing the
// corresponding object code.
func TestCompileGoPackage_RejectsUnsupportedSources(t *testing.T) {
	tests := []struct {
		name  string
		files gofiles.PkgFiles
		want  string
	}{
		{
			name:  "objective-c",
			files: gofiles.PkgFiles{GoFiles: []string{"a.go"}, MFiles: []string{"foo.m"}},
			want:  "Objective-C",
		},
		{
			name:  "swig",
			files: gofiles.PkgFiles{GoFiles: []string{"a.go"}, SwigFiles: []string{"foo.swig"}},
			want:  "SWIG",
		},
		{
			name:  "swigcxx",
			files: gofiles.PkgFiles{GoFiles: []string{"a.go"}, SwigCXXFiles: []string{"foo.swigcxx"}},
			want:  "SWIG",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.files
			err := CompileGoPackage(Options{
				ImportPath: "example.com/p",
				SrcDir:     t.TempDir(),
				Output:     t.TempDir() + "/out.a",
				Files:      &f,
			})
			if err == nil {
				t.Fatalf("expected error mentioning %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}
