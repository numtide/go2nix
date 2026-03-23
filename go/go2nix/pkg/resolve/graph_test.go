package resolve

import (
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/storepath"
	"github.com/numtide/go2nix/pkg/golist"
)

func TestTopoSort(t *testing.T) {
	pkgs := map[string]*ResolvedPkg{
		"a": {ImportPath: "a", Imports: []string{"b", "c"}},
		"b": {ImportPath: "b", Imports: []string{"c"}},
		"c": {ImportPath: "c", Imports: nil},
	}

	sorted, err := topoSort(pkgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3, got %d", len(sorted))
	}

	// c must come before b, b before a
	indexOf := func(ip string) int {
		for i, p := range sorted {
			if p.ImportPath == ip {
				return i
			}
		}
		return -1
	}

	if indexOf("c") > indexOf("b") {
		t.Error("c should come before b")
	}
	if indexOf("b") > indexOf("a") {
		t.Error("b should come before a")
	}
}

func TestTopoSortCycle(t *testing.T) {
	pkgs := map[string]*ResolvedPkg{
		"a": {ImportPath: "a", Imports: []string{"b"}},
		"b": {ImportPath: "b", Imports: []string{"a"}},
	}

	_, err := topoSort(pkgs)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestTopoSortDiamond(t *testing.T) {
	// Diamond dependency: a → b, a → c, b → d, c → d
	pkgs := map[string]*ResolvedPkg{
		"a": {ImportPath: "a", Imports: []string{"b", "c"}},
		"b": {ImportPath: "b", Imports: []string{"d"}},
		"c": {ImportPath: "c", Imports: []string{"d"}},
		"d": {ImportPath: "d", Imports: nil},
	}

	sorted, err := topoSort(pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 4 {
		t.Fatalf("expected 4, got %d", len(sorted))
	}

	indexOf := func(ip string) int {
		for i, p := range sorted {
			if p.ImportPath == ip {
				return i
			}
		}
		return -1
	}

	// d must come before b and c, which must come before a
	if indexOf("d") > indexOf("b") || indexOf("d") > indexOf("c") {
		t.Error("d should come before b and c")
	}
	if indexOf("b") > indexOf("a") || indexOf("c") > indexOf("a") {
		t.Error("b and c should come before a")
	}
}

func TestBuildPackageGraph(t *testing.T) {
	cryptoMod := &golist.Module{Path: "golang.org/x/crypto", Version: "v0.17.0"}
	fodPath, err := storepath.FromAbsolutePath("/nix/store/abc123abc123abc123abc123abc123ab-gomod-golang-org-x-crypto-v0-17-0")
	if err != nil {
		t.Fatal(err)
	}

	pkgs := []golist.Pkg{
		{
			ImportPath: "golang.org/x/crypto/ssh",
			Name:       "ssh",
			GoFiles:    []string{"client.go", "server.go"},
			Imports:    []string{"golang.org/x/crypto/internal/chacha20poly1305", "fmt"},
			Module:     cryptoMod,
		},
		{
			ImportPath: "golang.org/x/crypto/internal/chacha20poly1305",
			Name:       "chacha20poly1305",
			GoFiles:    []string{"chacha20poly1305.go"},
			Imports:    []string{"crypto/cipher"},
			Module:     cryptoMod,
		},
		{
			ImportPath: "mymod/cmd/myapp",
			Name:       "main",
			Dir:        "/nix/store/src/cmd/myapp",
			GoFiles:    []string{"main.go"},
			Imports:    []string{"golang.org/x/crypto/ssh", "fmt"},
			Module:     &golist.Module{Path: "mymod", Main: true},
		},
	}

	fodPaths := map[string]*storepath.StorePath{
		"golang.org/x/crypto@v0.17.0": fodPath,
	}

	graph, err := buildPackageGraph(pkgs, fodPaths, "/nix/store/src")
	if err != nil {
		t.Fatal(err)
	}

	// Check third-party package
	ssh := graph["golang.org/x/crypto/ssh"]
	if ssh == nil {
		t.Fatal("missing ssh package")
	}
	if ssh.IsLocal {
		t.Error("ssh should not be local")
	}
	if ssh.ModKey != "golang.org/x/crypto@v0.17.0" {
		t.Errorf("ssh modKey = %q", ssh.ModKey)
	}
	if ssh.ModPath != "golang.org/x/crypto" {
		t.Errorf("ssh modPath = %q", ssh.ModPath)
	}
	if ssh.Subdir != "ssh" {
		t.Errorf("ssh subdir = %q, want %q", ssh.Subdir, "ssh")
	}
	if ssh.FodPath.Absolute() != fodPath.Absolute() {
		t.Errorf("ssh fodPath = %q", ssh.FodPath.Absolute())
	}
	// Should have ALL imports (including stdlib)
	if len(ssh.Imports) != 2 {
		t.Errorf("ssh imports = %v, want 2 entries", ssh.Imports)
	}

	// Check local package
	myapp := graph["mymod/cmd/myapp"]
	if myapp == nil {
		t.Fatal("missing myapp package")
	}
	if !myapp.IsLocal {
		t.Error("myapp should be local")
	}
	if myapp.Name != "main" {
		t.Errorf("myapp name = %q", myapp.Name)
	}
	// Local packages should have computed Subdir from Dir
	if myapp.Subdir != "cmd/myapp" {
		t.Errorf("myapp subdir = %q, want %q", myapp.Subdir, "cmd/myapp")
	}
}

func TestBuildPackageGraph_LocalReplace(t *testing.T) {
	// Simulate: replace example.com/shared => ./libs/shared
	// Package example.com/shared/utils lives at /src/libs/shared/utils on disk.
	pkgs := []golist.Pkg{
		{
			ImportPath: "example.com/shared/utils",
			Name:       "utils",
			Dir:        "/src/libs/shared/utils",
			GoFiles:    []string{"utils.go"},
			Imports:    []string{"fmt"},
			Module: &golist.Module{
				Path:    "example.com/shared",
				Version: "v1.0.0",
				Replace: &golist.Replace{Path: "./libs/shared"},
			},
		},
		{
			ImportPath: "mymod/main",
			Name:       "main",
			Dir:        "/src",
			GoFiles:    []string{"main.go"},
			Imports:    []string{"example.com/shared/utils"},
			Module:     &golist.Module{Path: "mymod", Main: true},
		},
	}

	graph, err := buildPackageGraph(pkgs, nil, "/src")
	if err != nil {
		t.Fatal(err)
	}

	utils := graph["example.com/shared/utils"]
	if utils == nil {
		t.Fatal("missing utils package")
	}
	if !utils.IsLocal {
		t.Error("utils should be local (local replace)")
	}
	// Subdir must be relative to srcRoot, reflecting the actual filesystem layout,
	// NOT the import path structure.
	if utils.Subdir != "libs/shared/utils" {
		t.Errorf("utils subdir = %q, want %q", utils.Subdir, "libs/shared/utils")
	}
}

func TestBuildPackageGraph_MissingModule(t *testing.T) {
	pkgs := []golist.Pkg{
		{
			ImportPath: "golang.org/x/crypto/ssh",
			Name:       "ssh",
			GoFiles:    []string{"client.go"},
			Module:     &golist.Module{Path: "golang.org/x/crypto", Version: "v0.17.0"},
		},
	}

	// Empty fodPaths — module not in lockfile.
	_, err := buildPackageGraph(pkgs, map[string]*storepath.StorePath{}, "/src")
	if err == nil {
		t.Fatal("expected error for missing module")
	}
	if !strings.Contains(err.Error(), "golang.org/x/crypto@v0.17.0") {
		t.Errorf("error should mention module key, got: %s", err)
	}
}
