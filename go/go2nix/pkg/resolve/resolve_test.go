package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreDirOf(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"/nix/store/4zqp1bsg6mkia7c0xrh1f0gs3v9fk2jf-go/bin/go", "/nix/store/4zqp1bsg6mkia7c0xrh1f0gs3v9fk2jf-go"},
		{"/nix/store/abc123-cacert/etc/ssl/certs/ca-bundle.crt", "/nix/store/abc123-cacert"},
		{"/nix/store/xyz-go2nix/bin/go2nix", "/nix/store/xyz-go2nix"},
		// Bare store path (no sub-path)
		{"/nix/store/abc-src", "/nix/store/abc-src"},
		// Not a store path — returns input unchanged
		{"/usr/local/bin/go", "/usr/local/bin/go"},
	}
	for _, tt := range tests {
		if got := storeDirOf(tt.input); got != tt.want {
			t.Errorf("storeDirOf(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCollectStdlibImports(t *testing.T) {
	// Create a temporary stdlib directory with some .a files.
	dir := t.TempDir()
	for _, rel := range []string{
		"fmt.a",
		"net/http.a",
		"crypto/tls.a",
		"internal/poll.a",
		"vendor/golang.org/x/net/http2/hpack.a",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stdlib, err := collectStdlibImports(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Must include internal/ and vendor/ packages — the linker needs them
	// transitively (e.g., net/http depends on internal/poll).
	expected := []string{
		"crypto/tls",
		"fmt",
		"internal/poll",
		"net/http",
		"vendor/golang.org/x/net/http2/hpack",
	}
	if len(stdlib) != len(expected) {
		t.Fatalf("expected %d stdlib imports, got %d: %v", len(expected), len(stdlib), stdlib)
	}
	for i, want := range expected {
		if stdlib[i] != want {
			t.Errorf("stdlib[%d] = %q, want %q", i, stdlib[i], want)
		}
	}
}

func TestSymlinkTree(t *testing.T) {
	// Simulate two FOD outputs with overlapping directory prefixes.
	// FOD1: cache/download/golang.org/x/crypto/@v/v0.17.0.mod
	//        golang.org/x/crypto@v0.17.0/ssh/client.go
	// FOD2: cache/download/golang.org/x/text/@v/v0.14.0.mod
	//        golang.org/x/text@v0.14.0/unicode/unicode.go

	fod1 := t.TempDir()
	fod2 := t.TempDir()

	writeTestFile(t, filepath.Join(fod1, "cache/download/golang.org/x/crypto/@v/v0.17.0.mod"), "module crypto")
	writeTestFile(t, filepath.Join(fod1, "golang.org/x/crypto@v0.17.0/ssh/client.go"), "package ssh")

	writeTestFile(t, filepath.Join(fod2, "cache/download/golang.org/x/text/@v/v0.14.0.mod"), "module text")
	writeTestFile(t, filepath.Join(fod2, "golang.org/x/text@v0.14.0/unicode/unicode.go"), "package unicode")

	dst := t.TempDir()
	if err := symlinkTree(fod1, dst); err != nil {
		t.Fatal(err)
	}
	if err := symlinkTree(fod2, dst); err != nil {
		t.Fatal(err)
	}

	// Verify all files are reachable via symlinks in the merged tree.
	checks := []struct {
		path    string
		content string
	}{
		{"cache/download/golang.org/x/crypto/@v/v0.17.0.mod", "module crypto"},
		{"golang.org/x/crypto@v0.17.0/ssh/client.go", "package ssh"},
		{"cache/download/golang.org/x/text/@v/v0.14.0.mod", "module text"},
		{"golang.org/x/text@v0.14.0/unicode/unicode.go", "package unicode"},
	}
	for _, c := range checks {
		full := filepath.Join(dst, c.path)
		data, err := os.ReadFile(full)
		if err != nil {
			t.Errorf("reading %s: %v", c.path, err)
			continue
		}
		if string(data) != c.content {
			t.Errorf("%s: got %q, want %q", c.path, data, c.content)
		}

		// Verify it's a symlink, not a copy.
		info, err := os.Lstat(full)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s should be a symlink", c.path)
		}
	}

	// Verify intermediate directories are real dirs (not symlinks).
	dirChecks := []string{
		"cache",
		"cache/download",
		"cache/download/golang.org",
		"cache/download/golang.org/x",
		"golang.org",
		"golang.org/x",
	}
	for _, d := range dirChecks {
		full := filepath.Join(dst, d)
		info, err := os.Lstat(full)
		if err != nil {
			t.Errorf("stat %s: %v", d, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s should be a real directory, not a symlink", d)
		}
		if !info.IsDir() {
			t.Errorf("%s should be a directory", d)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
