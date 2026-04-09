package mvscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
[mod]
"github.com/foo/bar@v1.0.0" = "sha256-aaa"
"github.com/baz/qux/v2@v2.0.0" = "sha256-bbb"
"github.com/remote/replace@v3.0.0" = "sha256-ccc"
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

	t.Run("version-qualified replace not matching is ignored", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require (
	github.com/foo/bar v1.0.0
	github.com/local/mod v0.5.0
)
replace github.com/local/mod v0.9.9 => ../localdir
`)
		// Replace targets v0.9.9 but require is v0.5.0 — must NOT apply,
		// so local/mod@v0.5.0 is checked against the lockfile and missing.
		err := CheckLockfile(dir, lockfilePath)
		if err == nil {
			t.Fatal("expected error: version-qualified replace should not match v0.5.0")
		}
		if !strings.Contains(err.Error(), "github.com/local/mod@v0.5.0") {
			t.Errorf("error should mention github.com/local/mod@v0.5.0:\n%s", err)
		}
	})

	t.Run("wildcard replace wins over no match", func(t *testing.T) {
		writeFile("go.mod", `module example.com/test
go 1.23
require github.com/remote/replace v1.0.0
replace (
	github.com/remote/replace v0.9.9 => github.com/remote/replace v0.8.8
	github.com/remote/replace => github.com/remote/replace v3.0.0
)
`)
		// v0.9.9 directive does not match; wildcard does → v3.0.0, in lockfile.
		if err := CheckLockfile(dir, lockfilePath); err != nil {
			t.Fatalf("wildcard replace should apply: %v", err)
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
