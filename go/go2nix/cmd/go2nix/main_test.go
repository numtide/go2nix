package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUseLocalToolchain(t *testing.T) {
	tests := []struct {
		name string
		env  *string
		want string
	}{
		{name: "unset", env: nil, want: "local"},
		{name: "empty counts as unset", env: new(string), want: "local"},
		{name: "explicit auto is kept", env: new("auto"), want: "auto"},
		{name: "explicit version is kept", env: new("go1.26.0+auto"), want: "go1.26.0+auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv registers the restore; Unsetenv then gives the unset case.
			t.Setenv("GOTOOLCHAIN", "")
			if tt.env == nil {
				if err := os.Unsetenv("GOTOOLCHAIN"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("GOTOOLCHAIN", *tt.env)
			}
			useLocalToolchain()
			if got := os.Getenv("GOTOOLCHAIN"); got != tt.want {
				t.Errorf("GOTOOLCHAIN = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUseLocalToolchainIgnoresToolchainLine runs the real `go` the way the
// build subcommands do: offline, from a directory whose go.mod names a
// toolchain that does not exist.
func TestUseLocalToolchainIgnoresToolchainLine(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not found")
	}
	dir := t.TempDir()
	gomod := "module example.com/m\n\ngo 1.22\n\ntoolchain go1.99.0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	// GOENV=off keeps a developer's `go env -w GOTOOLCHAIN=...` out of it.
	t.Setenv("GOENV", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Setenv("GO111MODULE", "")

	goVersion := func() (string, error) {
		cmd := exec.Command("go", "env", "GOVERSION")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	t.Setenv("GOTOOLCHAIN", "auto")
	if out, err := goVersion(); err == nil {
		t.Fatalf("GOTOOLCHAIN=auto: go did not try to switch to go1.99.0 (got %q); the check below proves nothing", out)
	}

	t.Setenv("GOTOOLCHAIN", "")
	useLocalToolchain()
	out, err := goVersion()
	if err != nil {
		t.Fatalf("go env GOVERSION next to `toolchain go1.99.0`: %v\n%s", err, out)
	}
	if out == "go1.99.0" || !strings.HasPrefix(out, "go") {
		t.Errorf("go env GOVERSION = %q, want the version of the go on PATH", out)
	}
}
