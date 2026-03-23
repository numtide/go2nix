package localpkgs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/numtide/go2nix/pkg/toposort"
)

func TestIsLocalImport(t *testing.T) {
	prefixes := []string{"example.com/mymod", "example.com/other"}

	tests := []struct {
		imp  string
		want bool
	}{
		{"example.com/mymod", true},
		{"example.com/mymod/pkg/foo", true},
		{"example.com/other", true},
		{"example.com/other/bar", true},
		{"example.com/mymodextra", false}, // not a subpath
		{"example.com/unrelated", false},
		{"fmt", false},
	}

	for _, tt := range tests {
		if got := isLocalImport(tt.imp, prefixes); got != tt.want {
			t.Errorf("isLocalImport(%q) = %v, want %v", tt.imp, got, tt.want)
		}
	}
}

func TestTopoSort_Empty(t *testing.T) {
	deps := map[string][]string{}
	result, err := toposort.Sort(map[string]*LocalPkg{}, func(key string) []string {
		return deps[key]
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d entries", len(result))
	}
}

func TestTopoSort_Linear(t *testing.T) {
	// C -> B -> A (A has no deps)
	pkgs := map[string]*LocalPkg{
		"a": {ImportPath: "a"},
		"b": {ImportPath: "b"},
		"c": {ImportPath: "c"},
	}
	deps := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"b"},
	}
	result, err := toposort.Sort(pkgs, func(key string) []string {
		return deps[key]
	})
	if err != nil {
		t.Fatal(err)
	}

	paths := importPaths(result)
	idxA := indexOf(paths, "a")
	idxB := indexOf(paths, "b")
	idxC := indexOf(paths, "c")

	if idxA > idxB || idxB > idxC {
		t.Errorf("expected a before b before c, got order: %v", paths)
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	// d -> b, d -> c, b -> a, c -> a
	pkgs := map[string]*LocalPkg{
		"a": {ImportPath: "a"},
		"b": {ImportPath: "b"},
		"c": {ImportPath: "c"},
		"d": {ImportPath: "d"},
	}
	deps := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	result, err := toposort.Sort(pkgs, func(key string) []string {
		return deps[key]
	})
	if err != nil {
		t.Fatal(err)
	}

	paths := importPaths(result)
	idxA := indexOf(paths, "a")
	idxB := indexOf(paths, "b")
	idxC := indexOf(paths, "c")
	idxD := indexOf(paths, "d")

	if idxA > idxB || idxA > idxC || idxB > idxD || idxC > idxD {
		t.Errorf("topological order violated: %v", paths)
	}
}

func TestTopoSort_Cycle(t *testing.T) {
	pkgs := map[string]*LocalPkg{
		"a": {ImportPath: "a"},
		"b": {ImportPath: "b"},
	}
	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	_, err := toposort.Sort(pkgs, func(key string) []string {
		return deps[key]
	})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected 'cycle' in error, got: %v", err)
	}
}

func TestTopoSort_Deterministic(t *testing.T) {
	pkgs := map[string]*LocalPkg{
		"x": {ImportPath: "x"},
		"y": {ImportPath: "y"},
		"z": {ImportPath: "z"},
	}
	deps := map[string][]string{
		"x": {},
		"y": {},
		"z": {},
	}
	// Run multiple times — map iteration is non-deterministic, but output
	// should be stable because toposort.Sort sorts map keys.
	var first []string
	for i := 0; i < 20; i++ {
		result, err := toposort.Sort(pkgs, func(key string) []string {
			return deps[key]
		})
		if err != nil {
			t.Fatal(err)
		}
		paths := importPaths(result)
		if first == nil {
			first = paths
			continue
		}
		if strings.Join(paths, ",") != strings.Join(first, ",") {
			t.Fatalf("non-deterministic: run 0 = %v, run %d = %v", first, i, paths)
		}
	}
}

// writeFile is a test helper that creates a file with the given content,
// creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListLocalPackages_Basic(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nimport \"example.com/test/lib\"\n\nvar _ = lib.X\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "lib", "lib.go"), "package lib\n\nvar X = 1\n")

	pkgs, err := ListLocalPackages(root, "")
	if err != nil {
		t.Fatal(err)
	}

	paths := importPaths(pkgs)
	if len(paths) != 2 {
		t.Fatalf("expected 2 packages, got %d: %v", len(paths), paths)
	}

	// lib should come before main (main depends on lib)
	idxLib := indexOf(paths, "example.com/test/lib")
	idxMain := indexOf(paths, "example.com/test")
	if idxLib == -1 || idxMain == -1 {
		t.Fatalf("missing expected packages in %v", paths)
	}
	if idxLib > idxMain {
		t.Errorf("lib should come before main, got order: %v", paths)
	}
}

func TestListLocalPackages_SkipsVendorAndTestdata(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "vendor", "v.go"), "package vendor\n")
	writeFile(t, filepath.Join(root, "testdata", "t.go"), "package testdata\n")
	writeFile(t, filepath.Join(root, "_hidden", "h.go"), "package hidden\n")

	pkgs, err := ListLocalPackages(root, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range pkgs {
		if strings.Contains(p.ImportPath, "vendor") ||
			strings.Contains(p.ImportPath, "testdata") ||
			strings.Contains(p.ImportPath, "_hidden") {
			t.Errorf("should have skipped %s", p.ImportPath)
		}
	}
}

func TestListLocalPackages_NoGoMod(t *testing.T) {
	root := t.TempDir()
	_, err := ListLocalPackages(root, "")
	if err == nil {
		t.Fatal("expected error for missing go.mod, got nil")
	}
}

func importPaths(pkgs []*LocalPkg) []string {
	paths := make([]string, len(pkgs))
	for i, p := range pkgs {
		paths[i] = p.ImportPath
	}
	return paths
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestListLocalPackagesIncludesTestFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package test\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "foo_test.go"), []byte(`package test

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkgs, err := ListLocalPackages(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	pkg := pkgs[0]
	if len(pkg.TestGoFiles) == 0 {
		t.Error("expected TestGoFiles to be populated")
	}
	if pkg.TestGoFiles[0] != "foo_test.go" {
		t.Errorf("TestGoFiles[0] = %q, want foo_test.go", pkg.TestGoFiles[0])
	}
}
