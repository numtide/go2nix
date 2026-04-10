package buildinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateModinfo(t *testing.T) {
	// Create a temp directory with go.mod and go.sum.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module example.com/myapp

go 1.21

require golang.org/x/crypto v0.17.0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(`golang.org/x/crypto v0.17.0 h1:r8bRNjWMQoez8ZSjcgj4QGz/96WQOUj762vL2et/7AA=
golang.org/x/crypto v0.17.0/go.mod h1:gCAAfMLgwOJRpTjQ2zCCt2OcSfYMTeZVSRtQlPC7Nq4=
`), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := []ModDep{
		{Path: "golang.org/x/crypto", Version: "v0.17.0"},
	}

	line, err := GenerateModinfo(dir, "example.com/myapp/cmd/tool", "go1.21.5", deps, BuildSettings{
		BuildMode:   "exe",
		Tags:        "netgo,osusergo",
		CGOEnabled:  "0",
		GOARCH:      "amd64",
		GOARCHLevel: "v1",
		GOOS:        "linux",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(line, "modinfo ") {
		t.Errorf("expected modinfo prefix, got: %s", line)
	}
	// BuildInfo.Path = main package import path; Main.Path = module path.
	// `go version -m` renders these as `path\t…` and `mod\t…\t(devel)`.
	if !strings.Contains(line, "path\\texample.com/myapp/cmd/tool\\n") {
		t.Errorf("BuildInfo.Path should be main package import path:\n%s", line)
	}
	if !strings.Contains(line, "mod\\texample.com/myapp\\t(devel)") {
		t.Errorf("BuildInfo.Main.Path should be module path:\n%s", line)
	}
	if !strings.Contains(line, "golang.org/x/crypto") {
		t.Error("missing dependency in modinfo")
	}
	if !strings.Contains(line, "v0.17.0") {
		t.Error("missing version in modinfo")
	}
	if !strings.Contains(line, "h1:") {
		t.Error("missing go.sum hash in modinfo")
	}
	// Regression for finding #23: build settings must be embedded so
	// `go version -m` shows -compiler/CGO_ENABLED/GOOS/GOARCH/-tags lines.
	for _, want := range []string{
		"build\\t-buildmode=exe",
		"build\\t-compiler=gc",
		"build\\t-tags=netgo,osusergo",
		"build\\t-trimpath=true",
		"build\\tCGO_ENABLED=0",
		"build\\tGOARCH=amd64",
		"build\\tGOAMD64=v1",
		"build\\tGOOS=linux",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing build setting %q in modinfo:\n%s", want, line)
		}
	}
	// DefaultGODEBUG was empty — must be omitted.
	if strings.Contains(line, "DefaultGODEBUG") {
		t.Errorf("empty optional settings should be omitted:\n%s", line)
	}
}

// TestBuildSettingsMatchCmdGo locks the exact key set and order against
// cmd/go/internal/load/pkg.go setBuildInfo for a `go build -trimpath`
// invocation. Any future drift (extra keys, reordering, or reintroducing
// -ldflags) fails this golden.
func TestBuildSettingsMatchCmdGo(t *testing.T) {
	got := BuildSettings{
		BuildMode:      "exe",
		Tags:           "netgo",
		DefaultGODEBUG: "panicnil=1",
		CGOEnabled:     "0",
		GOARCH:         "amd64",
		GOARCHLevel:    "v1",
		GOOS:           "linux",
	}.toDebugSettings()

	want := []string{
		"-buildmode", "-compiler", "-tags", "-trimpath",
		"DefaultGODEBUG", "CGO_ENABLED", "GOARCH", "GOOS", "GOAMD64",
	}
	var gotKeys []string
	for _, s := range got {
		gotKeys = append(gotKeys, s.Key)
	}
	if strings.Join(gotKeys, ",") != strings.Join(want, ",") {
		t.Errorf("build setting keys diverge from cmd/go setBuildInfo (under -trimpath):\n  got:  %v\n  want: %v", gotKeys, want)
	}

	// With GOFIPS140 enabled it appears between GOARCH and GOOS
	// (cmd/go/internal/load/pkg.go:2477); "off" or "" must omit it.
	gotFips := BuildSettings{GOARCH: "amd64", GOFIPS140: "latest", GOOS: "linux"}.toDebugSettings()
	var fipsKeys []string
	for _, s := range gotFips {
		fipsKeys = append(fipsKeys, s.Key)
	}
	if strings.Join(fipsKeys, ",") != "-compiler,-trimpath,GOARCH,GOFIPS140,GOOS" {
		t.Errorf("GOFIPS140 position diverges from cmd/go: got %v", fipsKeys)
	}
	for _, v := range []string{"", "off"} {
		for _, s := range (BuildSettings{GOFIPS140: v}).toDebugSettings() {
			if s.Key == "GOFIPS140" {
				t.Errorf("GOFIPS140=%q must be omitted from build settings", v)
			}
		}
	}
	// Upstream omits -ldflags/-gcflags/-asmflags under -trimpath
	// (go.dev/issue/52372); never reintroduce.
	for _, k := range gotKeys {
		if k == "-ldflags" || k == "-gcflags" || k == "-asmflags" {
			t.Errorf("%s must not be emitted: cmd/go omits it under -trimpath", k)
		}
	}
}

func TestFips140Enabled(t *testing.T) {
	for _, tt := range []struct {
		v    string
		want bool
	}{
		{"", false},
		{"off", false},
		{"latest", true},
		{"v1.0.0", true},
		{"inprocess", true},
	} {
		if got := Fips140Enabled(tt.v); got != tt.want {
			t.Errorf("Fips140Enabled(%q) = %v, want %v", tt.v, got, tt.want)
		}
	}
}

func TestGenerateModinfoNoGoSum(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module example.com/myapp

go 1.21
`), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := []ModDep{
		{Path: "golang.org/x/net", Version: "v0.19.0"},
	}

	line, err := GenerateModinfo(dir, "", "go1.21.5", deps, BuildSettings{})
	if err != nil {
		t.Fatal(err)
	}

	// Should still work without go.sum, just without hashes.
	if !strings.Contains(line, "golang.org/x/net") {
		t.Error("missing dependency in modinfo")
	}
	// Empty mainPath falls back to module path.
	if !strings.Contains(line, "path\\texample.com/myapp\\n") {
		t.Errorf("empty mainPath should fall back to module path:\n%s", line)
	}
}

func TestGenerateModinfoWithReplace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module example.com/myapp

go 1.21
`), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := []ModDep{
		{
			Path:    "golang.org/x/crypto",
			Version: "v0.17.0",
			Replace: &ModDep{
				Path:    "github.com/fork/crypto",
				Version: "v0.17.0",
			},
		},
	}

	line, err := GenerateModinfo(dir, "example.com/myapp", "go1.21.5", deps, BuildSettings{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(line, "golang.org/x/crypto") {
		t.Error("missing original module path")
	}
	if !strings.Contains(line, "github.com/fork/crypto") {
		t.Error("missing replacement module path")
	}
}

func TestReadGoSum(t *testing.T) {
	dir := t.TempDir()
	sumFile := filepath.Join(dir, "go.sum")
	if err := os.WriteFile(sumFile, []byte(`golang.org/x/crypto v0.17.0 h1:abc123=
golang.org/x/crypto v0.17.0/go.mod h1:def456=
golang.org/x/net v0.19.0 h1:xyz789=
`), 0o644); err != nil {
		t.Fatal(err)
	}

	sums := readGoSum(sumFile)

	if h, ok := sums["golang.org/x/crypto@v0.17.0"]; !ok || h != "h1:abc123=" {
		t.Errorf("unexpected crypto hash: %q (ok=%v)", h, ok)
	}
	if h, ok := sums["golang.org/x/net@v0.19.0"]; !ok || h != "h1:xyz789=" {
		t.Errorf("unexpected net hash: %q (ok=%v)", h, ok)
	}
	// /go.mod entries should be excluded
	if _, ok := sums["golang.org/x/crypto@v0.17.0/go.mod"]; ok {
		t.Error("should not include /go.mod entries")
	}
}
