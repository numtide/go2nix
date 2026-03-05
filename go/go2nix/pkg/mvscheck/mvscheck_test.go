package mvscheck

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
	got := RequireVersions([]byte(fixture))
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
	got := ReplacedPaths([]byte(fixture))
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

// TestCheck exercises Check() against a real vendor tree and the real `go`
// toolchain. Tidy go.mod should pass; untidy should fail naming the version
// MVS wanted but the require list didn't have.
func TestCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("requires network + go toolchain")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}

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

	// Tidy + download.
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "download"}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}

	// Build gomod2nix-style symlink vendor tree.
	gomodcache := strings.TrimSpace(runOrDie(t, dir, "go", "env", "GOMODCACHE"))
	symlink := func(modPath, version string) {
		src := filepath.Join(gomodcache, modPath+"@"+version)
		dst := filepath.Join(dir, "vendor", modPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(src, dst); err != nil {
			t.Fatal(err)
		}
	}

	goModTidy, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	tidyReq := RequireVersions(goModTidy)
	for modPath, version := range tidyReq {
		symlink(modPath, version)
	}

	// Tidy go.mod should pass.
	if err := Check(dir); err != nil {
		t.Fatalf("Check() on tidy module: %v", err)
	}

	// Untidy: lower x/mod version to trigger MVS mismatch.
	xmodTidy, ok := tidyReq["golang.org/x/mod"]
	if !ok || xmodTidy == "v0.20.0" {
		t.Skipf("test module doesn't have the expected x/mod edge (got %q)", xmodTidy)
	}
	untidy := strings.Replace(string(goModTidy),
		"golang.org/x/mod "+xmodTidy,
		"golang.org/x/mod v0.20.0",
		1,
	)
	write("go.mod", untidy)

	// Re-vendor x/mod at v0.20.0.
	runOrDie(t, dir, "go", "mod", "download", "golang.org/x/mod@v0.20.0")
	if err := os.Remove(filepath.Join(dir, "vendor/golang.org/x/mod")); err != nil {
		t.Fatal(err)
	}
	symlink("golang.org/x/mod", "v0.20.0")

	err := Check(dir)
	if err == nil {
		t.Fatalf("Check() on untidy module: want error, got nil")
	}
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
