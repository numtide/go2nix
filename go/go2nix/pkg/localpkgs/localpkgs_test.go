package localpkgs

import (
	"os"
	"path/filepath"
	"slices"
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

	pkgs, err := ListLocalPackages(root, "", "")
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

	pkgs, err := ListLocalPackages(root, "", "")
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

func TestListLocalPackages_SkipsDotDirs(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, ".vscode", "x.go"), "package vscode\n")
	writeFile(t, filepath.Join(root, ".idea", "y.go"), "package idea\n")
	writeFile(t, filepath.Join(root, ".direnv", "z.go"), "package direnv\n")

	pkgs, err := ListLocalPackages(root, "", "")
	if err != nil {
		t.Fatal(err)
	}

	paths := importPaths(pkgs)
	if len(paths) != 1 || paths[0] != "example.com/test" {
		t.Fatalf("expected only root package, got %v", paths)
	}
}

func TestListLocalPackages_SkipsNestedModules(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/parent\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "lib", "lib.go"), "package lib\n")
	// Nested module: has its own go.mod, must not be reported as part of parent.
	writeFile(t, filepath.Join(root, "sub", "go.mod"), "module example.com/child\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "sub", "child.go"), "package child\n")
	writeFile(t, filepath.Join(root, "sub", "deeper", "d.go"), "package deeper\n")

	pkgs, err := ListLocalPackages(root, "", "")
	if err != nil {
		t.Fatal(err)
	}

	paths := importPaths(pkgs)
	for _, p := range paths {
		if strings.HasPrefix(p, "example.com/parent/sub") {
			t.Errorf("nested module package %q should have been skipped; got %v", p, paths)
		}
	}
	if indexOf(paths, "example.com/parent") == -1 {
		t.Errorf("expected root package in %v", paths)
	}
	if indexOf(paths, "example.com/parent/lib") == -1 {
		t.Errorf("expected sibling lib package in %v", paths)
	}
}

func TestListLocalPackages_NoGoMod(t *testing.T) {
	root := t.TempDir()
	_, err := ListLocalPackages(root, "", "")
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

	pkgs, err := ListLocalPackages(dir, "", "")
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

func TestListLocalPackages_TestEmbedFilesSorted(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "lib.go"), "package test\n")
	writeFile(t, filepath.Join(root, "lib_test.go"), `package test

import (
	"embed"
	"testing"
)

//go:embed *.txt
var fs embed.FS

func TestEmbed(t *testing.T) { _ = fs }
`)
	writeFile(t, filepath.Join(root, "ext_test.go"), `package test_test

import (
	"embed"
	"testing"
)

//go:embed *.txt
var fs embed.FS

func TestExtEmbed(t *testing.T) { _ = fs }
`)
	for _, name := range []string{"c.txt", "a.txt", "d.txt", "b.txt", "e.txt"} {
		writeFile(t, filepath.Join(root, name), "x")
	}

	pkgs, err := ListLocalPackages(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	pkg := pkgs[0]

	if !slices.Equal(pkg.TestEmbedPatterns, []string{"*.txt"}) {
		t.Errorf("TestEmbedPatterns = %v, want [*.txt]", pkg.TestEmbedPatterns)
	}
	if len(pkg.TestEmbedFiles) != 0 || pkg.TestEmbedCfg != nil {
		t.Errorf("TestEmbedFiles/Cfg populated before ResolveEmbeds: %v / %v", pkg.TestEmbedFiles, pkg.TestEmbedCfg)
	}

	if err := pkg.ResolveEmbeds(); err != nil {
		t.Fatal(err)
	}

	want := []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"}
	if !slices.Equal(pkg.TestEmbedFiles, want) {
		t.Errorf("TestEmbedFiles = %v, want %v (sorted)", pkg.TestEmbedFiles, want)
	}
	if !slices.Equal(pkg.XTestEmbedFiles, want) {
		t.Errorf("XTestEmbedFiles = %v, want %v (sorted)", pkg.XTestEmbedFiles, want)
	}
}

func TestModulePath(t *testing.T) {
	t.Run("module directive", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "go.mod"), "module example.com/myapp\n\ngo 1.21\n")
		got, err := ModulePath(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != "example.com/myapp" {
			t.Errorf("ModulePath = %q, want example.com/myapp", got)
		}
	})
	t.Run("no go.mod", func(t *testing.T) {
		_, err := ModulePath(t.TempDir())
		if err == nil {
			t.Fatal("expected error for missing go.mod, got nil")
		}
	})
	t.Run("no module directive", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "go.mod"), "go 1.21\n")
		got, err := ModulePath(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("ModulePath = %q, want empty (modfile.ModulePath returns \"\" when no module line)", got)
		}
	})
}

func TestResolveEmbeds(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "test.json", "x.yaml"} {
		writeFile(t, filepath.Join(dir, name), "x")
	}

	p := &LocalPkg{
		ImportPath:         "example.com/test",
		SrcDir:             dir,
		EmbedPatterns:      []string{"*.txt"},
		TestEmbedPatterns:  []string{"test.json"},
		XTestEmbedPatterns: []string{"x.yaml"},
	}
	if err := p.ResolveEmbeds(); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(p.EmbedFiles, []string{"a.txt", "b.txt"}) {
		t.Errorf("EmbedFiles = %v, want [a.txt b.txt]", p.EmbedFiles)
	}
	if p.EmbedCfg == nil {
		t.Fatal("EmbedCfg = nil")
	}
	if got := p.EmbedCfg.Patterns["*.txt"]; !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Errorf("EmbedCfg.Patterns[*.txt] = %v, want [a.txt b.txt]", got)
	}
	if p.EmbedCfg.Files["a.txt"] != filepath.Join(dir, "a.txt") {
		t.Errorf("EmbedCfg.Files[a.txt] = %q, want absolute path under SrcDir", p.EmbedCfg.Files["a.txt"])
	}

	if !slices.Equal(p.TestEmbedFiles, []string{"test.json"}) {
		t.Errorf("TestEmbedFiles = %v, want [test.json]", p.TestEmbedFiles)
	}
	if p.TestEmbedCfg == nil || p.TestEmbedCfg.Files["test.json"] != filepath.Join(dir, "test.json") {
		t.Errorf("TestEmbedCfg.Files[test.json] = %v", p.TestEmbedCfg)
	}

	if !slices.Equal(p.XTestEmbedFiles, []string{"x.yaml"}) {
		t.Errorf("XTestEmbedFiles = %v, want [x.yaml]", p.XTestEmbedFiles)
	}
	if p.XTestEmbedCfg == nil || p.XTestEmbedCfg.Files["x.yaml"] != filepath.Join(dir, "x.yaml") {
		t.Errorf("XTestEmbedCfg.Files[x.yaml] = %v", p.XTestEmbedCfg)
	}

	// Empty patterns yield nil cfg and an empty (not nil) file slice.
	empty := &LocalPkg{SrcDir: dir}
	if err := empty.ResolveEmbeds(); err != nil {
		t.Fatal(err)
	}
	if empty.EmbedCfg != nil || empty.EmbedFiles == nil || len(empty.EmbedFiles) != 0 {
		t.Errorf("empty patterns: EmbedCfg=%v EmbedFiles=%v, want nil / []", empty.EmbedCfg, empty.EmbedFiles)
	}
}

func TestListLocalPackages_DefersBrokenEmbed(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	// ui's //go:embed pattern matches nothing on disk; xtest's pattern is
	// likewise unsatisfied. Listing must succeed; ResolveEmbeds surfaces it.
	writeFile(t, filepath.Join(root, "ui", "ui.go"), `package ui

import "embed"

//go:embed all:dist
var Assets embed.FS
`)
	writeFile(t, filepath.Join(root, "ui", "ui_ext_test.go"), `package ui_test

import (
	_ "embed"
	"testing"
)

//go:embed missing.jsonc
var cfg string

func TestX(t *testing.T) { _ = cfg }
`)

	pkgs, err := ListLocalPackages(root, "", "")
	if err != nil {
		t.Fatalf("ListLocalPackages should defer broken embed resolution; got: %v", err)
	}

	var ui *LocalPkg
	for _, p := range pkgs {
		if p.ImportPath == "example.com/test/ui" {
			ui = p
		}
	}
	if ui == nil {
		t.Fatalf("ui package not listed: %v", importPaths(pkgs))
	}
	if !slices.Equal(ui.EmbedPatterns, []string{"all:dist"}) {
		t.Errorf("EmbedPatterns = %v, want [all:dist]", ui.EmbedPatterns)
	}
	if !slices.Equal(ui.XTestEmbedPatterns, []string{"missing.jsonc"}) {
		t.Errorf("XTestEmbedPatterns = %v, want [missing.jsonc]", ui.XTestEmbedPatterns)
	}

	err = ui.ResolveEmbeds()
	if err == nil {
		t.Fatal("ResolveEmbeds should surface the missing-match error")
	}
	if !strings.Contains(err.Error(), "no matching files found") {
		t.Errorf("expected 'no matching files found' in: %v", err)
	}
}
