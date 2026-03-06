package mvscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestCheckLockfile(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	lockfileTOML := `
[mod."github.com/foo/bar@v1.0.0"]
version = "v1.0.0"
hash = "sha256-aaa"
num_pkgs = 1

[mod."github.com/baz/qux/v2@v2.0.0"]
version = "v2.0.0"
hash = "sha256-bbb"
num_pkgs = 1

[mod."github.com/remote/replace@v3.0.0"]
version = "v3.0.0"
hash = "sha256-ccc"
num_pkgs = 1
`
	lockfilePath := filepath.Join(dir, "go2nix.lock")
	writeFile("go2nix.lock", lockfileTOML)

	t.Run("all present", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require (
	github.com/foo/bar v1.0.0
	github.com/baz/qux/v2 v2.0.0
)
`)
		if err := CheckLockfile(dir, lockfilePath); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing module", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require (
	github.com/foo/bar v1.0.0
	github.com/not/inlock v0.5.0
)
`)
		err := CheckLockfile(dir, lockfilePath)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "github.com/not/inlock@v0.5.0") {
			t.Errorf("error should mention missing module:\n%s", err)
		}
	})

	t.Run("local replace skipped", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require (
	github.com/foo/bar v1.0.0
	github.com/local/mod v0.0.0
)
replace github.com/local/mod => ../localdir
`)
		if err := CheckLockfile(dir, lockfilePath); err != nil {
			t.Fatalf("local replace should be skipped: %v", err)
		}
	})

	t.Run("versioned replace uses replacement version", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require (
	github.com/foo/bar v1.0.0
	github.com/remote/replace v1.0.0
)
replace github.com/remote/replace v1.0.0 => github.com/remote/replace v3.0.0
`)
		// Lockfile has github.com/remote/replace@v3.0.0 but not @v1.0.0.
		if err := CheckLockfile(dir, lockfilePath); err != nil {
			t.Fatalf("versioned replace should use effective version v3.0.0: %v", err)
		}
	})

	t.Run("multiple missing sorted", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require (
	github.com/zzz/last v1.0.0
	github.com/aaa/first v1.0.0
)
`)
		err := CheckLockfile(dir, lockfilePath)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msg := err.Error()
		idxA := strings.Index(msg, "github.com/aaa/first@v1.0.0")
		idxZ := strings.Index(msg, "github.com/zzz/last@v1.0.0")
		if idxA < 0 || idxZ < 0 {
			t.Fatalf("error should list both missing modules:\n%s", msg)
		}
		if idxA > idxZ {
			t.Errorf("missing modules should be sorted, got aaa at %d, zzz at %d:\n%s", idxA, idxZ, msg)
		}
	})
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
	write("go.mod", `module example.com/mvscheck-test
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
	mf, err := modfile.Parse("go.mod", goModTidy, nil)
	if err != nil {
		t.Fatalf("parsing tidied go.mod: %v", err)
	}
	for _, req := range mf.Require {
		symlink(req.Mod.Path, req.Mod.Version)
	}

	// Tidy go.mod should pass.
	if err := Check(dir); err != nil {
		t.Fatalf("Check() on tidy module: %v", err)
	}

	// Untidy: lower x/mod version to trigger MVS mismatch.
	var xmodTidy string
	for _, req := range mf.Require {
		if req.Mod.Path == "golang.org/x/mod" {
			xmodTidy = req.Mod.Version
			break
		}
	}
	if xmodTidy == "" || xmodTidy == "v0.20.0" {
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

	err = Check(dir)
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
