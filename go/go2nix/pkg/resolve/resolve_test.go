package resolve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nix-community/go-nix/pkg/storepath"
	"github.com/numtide/go2nix/pkg/nixdrv"
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

// TestLoadGoEnv verifies a single go env -json subprocess yields the keys
// previously fetched one-by-one via queryGoEnv.
func TestLoadGoEnv(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not found")
	}
	env := loadGoEnv(goBin)
	for _, k := range []string{"GOOS", "GOARCH", "GOROOT", "GOVERSION"} {
		if env[k] == "" {
			t.Errorf("loadGoEnv missing %s", k)
		}
	}
}

// TestTransitiveClosure verifies that only packages reachable from the root
// are included and that the result is deterministically sorted.
func TestTransitiveClosure(t *testing.T) {
	graph := map[string]*ResolvedPkg{
		"m/cmd/a":  {ImportPath: "m/cmd/a", Name: "main", Imports: []string{"m/lib/x", "fmt"}},
		"m/cmd/b":  {ImportPath: "m/cmd/b", Name: "main", Imports: []string{"m/lib/y"}},
		"m/lib/x":  {ImportPath: "m/lib/x", Imports: []string{"m/lib/z", "strings"}},
		"m/lib/y":  {ImportPath: "m/lib/y", Imports: []string{"m/lib/z"}},
		"m/lib/z":  {ImportPath: "m/lib/z", Imports: []string{"os"}},
		"m/unused": {ImportPath: "m/unused", Imports: nil},
	}

	got := transitiveClosure(graph, graph["m/cmd/a"])
	var paths []string
	for _, p := range got {
		paths = append(paths, p.ImportPath)
	}
	want := []string{"m/cmd/a", "m/lib/x", "m/lib/z"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("closure(a) = %v, want %v", paths, want)
	}

	// cmd/b's closure must not pull in m/lib/x or m/unused.
	got = transitiveClosure(graph, graph["m/cmd/b"])
	paths = nil
	for _, p := range got {
		paths = append(paths, p.ImportPath)
	}
	want = []string{"m/cmd/b", "m/lib/y", "m/lib/z"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("closure(b) = %v, want %v", paths, want)
	}
}

// TestBuildLinkDrvClosureOnly is a regression test for finding #16: a link drv
// must depend only on the main package's transitive closure, not every package
// in the graph.
func TestBuildLinkDrvClosureOnly(t *testing.T) {
	mkDrv := func(hash string) *storepath.StorePath {
		sp, err := storepath.FromAbsolutePath("/nix/store/" + hash + "-pkg.drv")
		if err != nil {
			t.Fatal(err)
		}
		return sp
	}
	graph := map[string]*ResolvedPkg{
		"m/cmd/a": {
			ImportPath: "m/cmd/a", Name: "main",
			Imports: []string{"m/lib/x", "fmt"},
			DrvPath: mkDrv("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
		"m/lib/x": {
			ImportPath: "m/lib/x", Imports: nil,
			DrvPath: mkDrv("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
		},
		"m/lib/unrelated": {
			ImportPath: "m/lib/unrelated", Imports: nil,
			CgoFiles: []string{"c.go"}, // cgo in an unrelated pkg must not flip extld
			DrvPath:  mkDrv("yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"),
		},
	}
	cfg := Config{
		PName:        "a",
		System:       "x86_64-linux",
		BashBin:      "/nix/store/00000000000000000000000000000000-bash/bin/bash",
		CoreutilsBin: "/nix/store/00000000000000000000000000000000-coreutils/bin/mkdir",
		GoBin:        "/nix/store/00000000000000000000000000000000-go/bin/go",
		StdlibPath:   "/nix/store/00000000000000000000000000000000-stdlib",
		ccPath:       "/nix/store/00000000000000000000000000000000-cc/bin/cc",
		ccDir:        "/nix/store/00000000000000000000000000000000-cc",
	}
	cfg.coreutilsDir = storeDirOf(cfg.CoreutilsBin)
	cfg.goEnv = map[string]string{}

	_, drv, err := buildLinkDrv(cfg, graph, nil, graph["m/cmd/a"], 1, nil, "")
	if err != nil {
		t.Fatalf("buildLinkDrv: %v", err)
	}
	entries := drv.Env()["importcfg_entries"]
	if !strings.Contains(entries, "packagefile m/lib/x=") {
		t.Errorf("importcfg missing direct dep m/lib/x:\n%s", entries)
	}
	if strings.Contains(entries, "m/lib/unrelated") {
		t.Errorf("importcfg references unrelated package:\n%s", entries)
	}
	if drv.Env()["extld"] != "" {
		t.Errorf("extld set despite no cgo in closure: %q", drv.Env()["extld"])
	}
}

// fakeStore is a minimal nixdrv.Store for unit-testing stageLocalSources.
type fakeStore struct {
	mu    sync.Mutex
	calls int32
	added map[string]string // name → source dir
}

func (s *fakeStore) DerivationAdd(*nixdrv.Derivation) (*storepath.StorePath, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *fakeStore) Build(...string) ([]*storepath.StorePath, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *fakeStore) StoreAdd(name, path string) (*storepath.StorePath, error) {
	atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	if s.added == nil {
		s.added = map[string]string{}
	}
	s.added[name] = path
	s.mu.Unlock()
	hash := strings.Repeat("a", 32)
	return storepath.FromAbsolutePath("/nix/store/" + hash + "-" + name)
}

// TestStageLocalSources verifies that local-package sources are staged in
// parallel and that third-party packages are skipped.
func TestStageLocalSources(t *testing.T) {
	root := t.TempDir()
	mkPkg := func(subdir string) {
		writeTestFile(t, filepath.Join(root, subdir, "main.go"), "package p")
	}
	mkPkg("a")
	mkPkg("b")
	mkPkg("c")

	pkgs := []*ResolvedPkg{
		{ImportPath: "m/a", IsLocal: true, Subdir: "a", GoFiles: []string{"main.go"}},
		{ImportPath: "m/b", IsLocal: true, Subdir: "b", GoFiles: []string{"main.go"}},
		{ImportPath: "m/c", IsLocal: true, Subdir: "c", GoFiles: []string{"main.go"}},
		{ImportPath: "ext/d", IsLocal: false},
	}
	fs := &fakeStore{}
	cfg := Config{Src: root, NixJobs: 4}
	n, err := stageLocalSources(cfg, fs, pkgs)
	if err != nil {
		t.Fatalf("stageLocalSources: %v", err)
	}
	if n != 3 {
		t.Fatalf("staged count = %d, want 3", n)
	}
	if fs.calls != 3 {
		t.Fatalf("StoreAdd calls = %d, want 3", fs.calls)
	}
	for _, p := range pkgs[:3] {
		if p.SrcStorePath == nil {
			t.Errorf("%s: SrcStorePath not set", p.ImportPath)
		}
	}
	if pkgs[3].SrcStorePath != nil {
		t.Error("third-party package should not be staged")
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
