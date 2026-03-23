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

func TestDiscoverInputPaths(t *testing.T) {
	// Create a fake store layout:
	//   pkgA/bin/          (has bin)
	//   pkgA/lib/pkgconfig/ (has pkgconfig)
	//   pkgA/nix-support/propagated-build-inputs → "pkgB"
	//   pkgB/lib/pkgconfig/ (has pkgconfig, no bin)
	//   pkgC/bin/           (has bin, no pkgconfig, no propagated)

	root := t.TempDir()
	pkgA := filepath.Join(root, "pkgA")
	pkgB := filepath.Join(root, "pkgB")
	pkgC := filepath.Join(root, "pkgC")

	// pkgA: bin + pkgconfig + propagated-build-inputs → pkgB
	if err := os.MkdirAll(filepath.Join(pkgA, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgA, "lib", "pkgconfig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgA, "nix-support"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgA, "nix-support", "propagated-build-inputs"),
		[]byte(pkgB), 0o644); err != nil {
		t.Fatal(err)
	}

	// pkgB: pkgconfig only
	if err := os.MkdirAll(filepath.Join(pkgB, "lib", "pkgconfig"), 0o755); err != nil {
		t.Fatal(err)
	}

	// pkgC: bin only
	if err := os.MkdirAll(filepath.Join(pkgC, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := discoverInputPaths([]string{pkgA, pkgC})

	// BinDirs: pkgA/bin, pkgC/bin
	if len(result.BinDirs) != 2 {
		t.Fatalf("expected 2 BinDirs, got %d: %v", len(result.BinDirs), result.BinDirs)
	}
	if result.BinDirs[0] != pkgA+"/bin" || result.BinDirs[1] != pkgC+"/bin" {
		t.Errorf("BinDirs = %v, want [%s %s]", result.BinDirs, pkgA+"/bin", pkgC+"/bin")
	}

	// PkgConfigDirs: pkgA/lib/pkgconfig, pkgB/lib/pkgconfig (transitive)
	if len(result.PkgConfigDirs) != 2 {
		t.Fatalf("expected 2 PkgConfigDirs, got %d: %v", len(result.PkgConfigDirs), result.PkgConfigDirs)
	}
	if result.PkgConfigDirs[0] != pkgA+"/lib/pkgconfig" || result.PkgConfigDirs[1] != pkgB+"/lib/pkgconfig" {
		t.Errorf("PkgConfigDirs = %v, want [%s %s]", result.PkgConfigDirs,
			pkgA+"/lib/pkgconfig", pkgB+"/lib/pkgconfig")
	}

	// All: pkgA, pkgB (transitive), pkgC — in walk order
	if len(result.All) != 3 {
		t.Fatalf("expected 3 All, got %d: %v", len(result.All), result.All)
	}
	if result.All[0] != pkgA || result.All[1] != pkgB || result.All[2] != pkgC {
		t.Errorf("All = %v, want [%s %s %s]", result.All, pkgA, pkgB, pkgC)
	}
}

func TestDiscoverInputPathsCyclic(t *testing.T) {
	// Ensure cycles in propagated-build-inputs are handled.
	root := t.TempDir()
	pkgX := filepath.Join(root, "pkgX")
	pkgY := filepath.Join(root, "pkgY")

	if err := os.MkdirAll(filepath.Join(pkgX, "nix-support"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgX, "nix-support", "propagated-build-inputs"),
		[]byte(pkgY), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(pkgY, "nix-support"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgY, "nix-support", "propagated-build-inputs"),
		[]byte(pkgX), 0o644); err != nil {
		t.Fatal(err)
	}

	result := discoverInputPaths([]string{pkgX})
	if len(result.All) != 2 {
		t.Fatalf("expected 2 All (cycle handled), got %d: %v", len(result.All), result.All)
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
