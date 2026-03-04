// Command go2nix generates a Nix lockfile for Go projects.
//
// The lockfile has two sections:
//
//   - [mod.*] maps module@version to NAR hashes for fetching via fetchModuleProxy.
//   - [pkg.*] maps import paths to their containing module and direct imports,
//     defining the package-level DAG for per-package Nix derivations.
//
// The source of truth is `go list -json -deps ./...`, which produces the exact
// package graph needed for a build target — no test dependencies of dependencies,
// no unused modules, no cycles (the package graph is always a DAG).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/nix-community/go-nix/pkg/nar"
	"golang.org/x/sync/errgroup"
)

// --- Lockfile schema ---------------------------------------------------------

// ModEntry is one module in the lockfile [mod.*] section.
type ModEntry struct {
	Version  string `toml:"version"`
	Hash     string `toml:"hash"`                // SRI NAR hash, e.g. "sha256-..."
	Replaced string `toml:"replaced,omitempty"`   // fetch path if different from key path
	NumPkgs  int    `toml:"num_pkgs"`             // number of packages from this module
}

// PkgEntry is one package in the lockfile [pkg.*] section.
type PkgEntry struct {
	Module  string   `toml:"module"`            // module key, e.g. "golang.org/x/net@v0.30.0"
	Imports []string `toml:"imports,omitempty"`  // direct non-stdlib imports
}

type lockfile struct {
	Mod map[string]ModEntry `toml:"mod"`
	Pkg map[string]PkgEntry `toml:"pkg"`
}

func readLockfile(path string) (*lockfile, error) {
	lf := &lockfile{Mod: map[string]ModEntry{}, Pkg: map[string]PkgEntry{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return lf, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := toml.Decode(string(data), lf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return lf, nil
}

func (lf *lockfile) write(path, header string) error {
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := toml.NewEncoder(&buf).Encode(lf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// --- Package graph collection ------------------------------------------------

// goListPkg matches one JSON record from `go list -json -deps`.
type goListPkg struct {
	ImportPath string   `json:"ImportPath"`
	Name       string   `json:"Name"`
	GoFiles    []string `json:"GoFiles"`
	CgoFiles   []string `json:"CgoFiles"`
	Standard   bool     `json:"Standard"`
	DepOnly    bool     `json:"DepOnly"`
	Imports    []string `json:"Imports"`

	Module *goListModule `json:"Module"`
}

type goListModule struct {
	Path    string         `json:"Path"`
	Version string         `json:"Version"`
	Main    bool           `json:"Main"`
	Replace *goListReplace `json:"Replace"`
}

type goListReplace struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

// isLocal reports whether this module is local (main module or local replace).
func (m *goListModule) isLocal() bool {
	if m == nil || m.Main {
		return true
	}
	// Local replace: Replace exists but has no version.
	if m.Replace != nil && m.Replace.Version == "" {
		return true
	}
	return false
}

// collectPackages runs `go list -json -deps ./...` in dir and returns all
// non-stdlib, non-main-module packages.
func collectPackages(dir string) ([]goListPkg, error) {
	cmd := exec.Command("go", "list", "-json", "-deps", "./...")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -json -deps in %s: %w", dir, err)
	}

	var pkgs []goListPkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goListPkg
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}

		// Skip stdlib packages — they're compiled from GOROOT.
		if pkg.Standard {
			continue
		}
		// Skip local modules (main module + local replace directives).
		if pkg.Module.isLocal() {
			continue
		}

		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// --- Module collection from packages -----------------------------------------

// modInfo describes a module to fetch and hash.
type modInfo struct {
	key       string // "path@version", lockfile key
	fetchPath string // path to fetch (differs from key for replaces)
	version   string
}

// replaced returns the fetch path if it differs from the key's path component.
func (m modInfo) replaced() string {
	origPath := strings.TrimSuffix(m.key, "@"+m.version)
	if origPath != m.fetchPath {
		return m.fetchPath
	}
	return ""
}

// collectModulesFromPackages extracts unique modules from the package list.
func collectModulesFromPackages(pkgs []goListPkg) []modInfo {
	seen := map[string]bool{}
	var mods []modInfo
	for _, pkg := range pkgs {
		if pkg.Module == nil || pkg.Module.Version == "" {
			continue
		}

		// For remote replaces, use the replacement's path and version for
		// fetching. The key uses the original path (what go.mod `require`
		// refers to) + the replacement version (what's actually resolved).
		fetchPath := pkg.Module.Path
		version := pkg.Module.Version
		if r := pkg.Module.Replace; r != nil && r.Version != "" {
			fetchPath = r.Path
			version = r.Version
		}

		key := pkg.Module.Path + "@" + version
		if seen[key] {
			continue
		}
		seen[key] = true
		mods = append(mods, modInfo{
			key:       key,
			fetchPath: fetchPath,
			version:   version,
		})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].key < mods[j].key })
	return mods
}

// --- Hashing -----------------------------------------------------------------

// modCacheHash computes the NAR hash of a module's GOMODCACHE output, matching
// what fetchModuleProxy produces in the Nix sandbox.
func modCacheHash(fetchPath, version string) (string, error) {
	tmpdir, err := os.MkdirTemp("", "go2nix-hash-")
	if err != nil {
		return "", err
	}
	defer removeReadOnly(tmpdir)

	cmd := exec.Command("go", "mod", "download", fetchPath+"@"+version)
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+tmpdir,
		"GONOSUMCHECK=*",
		"GONOSUMDB=*",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go mod download %s@%s: %w\n%s", fetchPath, version, err, out)
	}

	return narHashPath(tmpdir)
}

// narHashPath computes the SRI NAR hash of a path, equivalent to `nix hash path`.
func narHashPath(path string) (string, error) {
	h := sha256.New()
	if err := nar.DumpPath(h, path); err != nil {
		return "", fmt.Errorf("nar hash %s: %w", path, err)
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// removeReadOnly removes a directory tree that may contain read-only files
// (Go module cache makes source files 0444).
func removeReadOnly(dir string) {
	filepath.WalkDir(dir, func(path string, _ fs.DirEntry, _ error) error {
		os.Chmod(path, 0o755)
		return nil
	})
	os.RemoveAll(dir)
}

// --- Main --------------------------------------------------------------------

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		generateCmd(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "generate":
		generateCmd(os.Args[2:])
	case "list-files":
		listFilesCmd(os.Args[2:])
	case "list-local-packages":
		listLocalPackagesCmd(os.Args[2:])
	default:
		generateCmd(os.Args[1:])
	}
}

func generateCmd(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	output := fs.String("o", "go2nix.toml", "output lockfile path")
	jobs := fs.Int("j", runtime.NumCPU(), "max parallel hash invocations")
	fs.Parse(args)

	dirs := fs.Args()
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	if err := generate(dirs, *output, *jobs); err != nil {
		log.Fatal(err)
	}
}

func generate(dirs []string, output string, jobs int) error {
	cache, err := readLockfile(output)
	if err != nil {
		return fmt.Errorf("reading existing lockfile: %w", err)
	}
	log.Printf("cache: %d mod entries, %d pkg entries", len(cache.Mod), len(cache.Pkg))

	// Collect packages from all project directories.
	allPkgMap := map[string]goListPkg{} // importPath -> pkg
	for _, dir := range dirs {
		log.Printf("collecting packages from %s ...", dir)
		pkgs, err := collectPackages(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		for _, pkg := range pkgs {
			if _, ok := allPkgMap[pkg.ImportPath]; !ok {
				allPkgMap[pkg.ImportPath] = pkg
			}
		}
	}
	log.Printf("found %d unique third-party packages", len(allPkgMap))

	// Build the set of all third-party import paths for filtering imports.
	thirdPartyPkgs := map[string]bool{}
	for ip := range allPkgMap {
		thirdPartyPkgs[ip] = true
	}

	// Extract unique modules from the package graph.
	allPkgs := make([]goListPkg, 0, len(allPkgMap))
	for _, pkg := range allPkgMap {
		allPkgs = append(allPkgs, pkg)
	}
	mods := collectModulesFromPackages(allPkgs)
	log.Printf("found %d unique modules", len(mods))

	// Count packages per module.
	pkgsPerMod := map[string]int{}
	for _, pkg := range allPkgs {
		if pkg.Module != nil && pkg.Module.Version != "" {
			v := pkg.Module.Version
			if r := pkg.Module.Replace; r != nil && r.Version != "" {
				v = r.Version
			}
			pkgsPerMod[pkg.Module.Path+"@"+v]++
		}
	}

	// Determine which modules need hashing (check cache).
	var toHash []modInfo
	resultMod := map[string]ModEntry{}
	for _, m := range mods {
		if cached, ok := cache.Mod[m.key]; ok && cached.Replaced == m.replaced() {
			cached.NumPkgs = pkgsPerMod[m.key]
			resultMod[m.key] = cached
		} else {
			toHash = append(toHash, m)
		}
	}
	slices.SortFunc(toHash, func(a, b modInfo) int { return strings.Compare(a.key, b.key) })
	log.Printf("hash: %d modules (%d cached)", len(toHash), len(resultMod))

	// Hash modules in parallel.
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(jobs)
	for _, m := range toHash {
		g.Go(func() error {
			h, err := modCacheHash(m.fetchPath, m.version)
			if err != nil {
				return fmt.Errorf("hashing %s: %w", m.key, err)
			}
			mu.Lock()
			resultMod[m.key] = ModEntry{
				Version:  m.version,
				Hash:     h,
				Replaced: m.replaced(),
				NumPkgs:  pkgsPerMod[m.key],
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Build package entries.
	resultPkg := map[string]PkgEntry{}
	for _, pkg := range allPkgs {
		if pkg.Module == nil || pkg.Module.Version == "" {
			continue
		}
		// Use the resolved version (replacement version if replaced).
		version := pkg.Module.Version
		if r := pkg.Module.Replace; r != nil && r.Version != "" {
			version = r.Version
		}
		modKey := pkg.Module.Path + "@" + version

		// Filter imports to only third-party packages (not stdlib).
		var imports []string
		for _, imp := range pkg.Imports {
			if thirdPartyPkgs[imp] {
				imports = append(imports, imp)
			}
		}
		sort.Strings(imports)

		resultPkg[pkg.ImportPath] = PkgEntry{
			Module:  modKey,
			Imports: imports,
		}
	}

	result := &lockfile{Mod: resultMod, Pkg: resultPkg}
	log.Printf("write: %d modules, %d packages -> %s", len(resultMod), len(resultPkg), output)
	return result.write(output, lockfileHeader)
}

const lockfileHeader = `# go2nix lockfile — package-level build graph.
# [mod.*]: module@version -> NAR hash (for fetchModuleProxy FODs)
# [pkg.*]: import path -> module + direct imports (for per-package derivations)
# Generated by go2nix. Do not edit.

`
