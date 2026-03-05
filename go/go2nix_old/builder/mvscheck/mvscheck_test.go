package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireVersions(t *testing.T) {
	fixture := `module test
go 1.23
require github.com/single/line v1.0.0
require (
	github.com/in/paren v2.0.0
	github.com/with/comment v3.0.0 // indirect
)
replace github.com/replaced => ../local
`
	got := requireVersions([]byte(fixture))
	want := map[string]string{
		"github.com/single/line":  "v1.0.0",
		"github.com/in/paren":     "v2.0.0",
		"github.com/with/comment": "v3.0.0",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("path %q: got %q, want %q", k, got[k], v)
		}
	}
}

func TestReplacedPaths(t *testing.T) {
	fixture := `module test
go 1.23
require github.com/foo/bar v1.0.0
replace github.com/single => ../local
replace (
	github.com/in/paren => github.com/fork v2.0.0
	github.com/with/version v1.0.0 => ../another
)
`
	got := replacedPaths([]byte(fixture))
	want := []string{"github.com/single", "github.com/in/paren", "github.com/with/version"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("path %q: not in replaced set", p)
		}
	}
}

// TestCheck exercises check() against a real vendor tree and the real `go`
// toolchain. Tidy go.mod should pass; untidy should fail with a message that
// names the module-version MVS wanted but the require list didn't have.
//
// This is the only real validation that `go mod graph` + fake-GOMODCACHE
// behaves the way we claim. The test pins specific versions of golang.org/x/*
// — if those stop resolving, update the pins.
func TestCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("requires network + go toolchain")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}

	// Build a tidy module with one direct dep that has a transitive dep we
	// can later lower to make go.mod untidy.
	// x/tools v0.30.0 requires x/mod v0.23.0 — this is the edge we'll break.
	dir := t.TempDir()
	write := func(name, content string) {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", `module mvscheck-test
go 1.23
require golang.org/x/tools v0.30.0
`)
	write("main.go", `package main
import _ "golang.org/x/tools/go/packages"
func main() {}
`)

	// Tidy + download to populate go.sum and the module cache.
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "download"}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}

	// Find the module cache to build a gomod2nix-style symlink vendor tree.
	gomodcache := strings.TrimSpace(
		runOrDie(t, dir, "go", "env", "GOMODCACHE"),
	)
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	symlink := func(modPath, version string) {
		src := filepath.Join(gomodcache, modPath+"@"+version)
		dst := filepath.Join(dir, "vendor", modPath)
		mkdir(filepath.Dir(filepath.Join("vendor", modPath)))
		if err := os.Symlink(src, dst); err != nil {
			t.Fatal(err)
		}
	}

	// --- TIDY: vendor the versions go.mod tidy produced.
	goModTidy, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	tidyReq := requireVersions(goModTidy)
	for modPath, version := range tidyReq {
		symlink(modPath, version)
	}

	chdir(t, dir)
	if err := check(t.TempDir()); err != nil {
		t.Fatalf("check() on tidy module: %v", err)
	}

	// --- UNTIDY: lower x/mod in go.mod, re-vendor with the lower version
	// (simulating the accidental-index-hit scenario where the shared lockfile
	// happened to have this lower version from another project).
	xmodTidy, ok := tidyReq["golang.org/x/mod"]
	if !ok || xmodTidy == "v0.20.0" {
		// If x/tools ever drops its x/mod dep or it's already at v0.20.0,
		// this test loses its teeth. Skip rather than false-pass.
		t.Skipf("test module doesn't have the expected x/mod edge (got %q)", xmodTidy)
	}
	untidy := strings.Replace(string(goModTidy),
		"golang.org/x/mod "+xmodTidy,
		"golang.org/x/mod v0.20.0",
		1,
	)
	write("go.mod", untidy)

	// Re-vendor x/mod at v0.20.0. Need to download it first.
	runOrDie(t, dir, "go", "mod", "download", "golang.org/x/mod@v0.20.0")
	if err := os.Remove(filepath.Join(dir, "vendor/golang.org/x/mod")); err != nil {
		t.Fatal(err)
	}
	symlink("golang.org/x/mod", "v0.20.0")

	err := check(t.TempDir())
	if err == nil {
		t.Fatalf("check() on untidy module: want error, got nil")
	}
	// The error should mention x/mod and the version MVS wanted.
	for _, want := range []string{"golang.org/x/mod", xmodTidy, "not tidy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err)
		}
	}
}

func runOrDie(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
