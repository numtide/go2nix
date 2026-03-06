// Package lockfilegen produces go2nix lockfiles from Go projects.
package lockfilegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nix-community/go-nix/pkg/nar"
	"github.com/numtide/go2nix/pkg/lockfile"
	"golang.org/x/sync/errgroup"
)

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

func (m *goListModule) isLocal() bool {
	if m == nil || m.Main {
		return true
	}
	if m.Replace != nil && m.Replace.Version == "" {
		return true
	}
	return false
}

type modInfo struct {
	key       string
	fetchPath string
	version   string
}

func (m modInfo) replaced() string {
	origPath := strings.TrimSuffix(m.key, "@"+m.version)
	if origPath != m.fetchPath {
		return m.fetchPath
	}
	return ""
}

// Generate creates a go2nix lockfile from the given project directories.
func Generate(dirs []string, output string, jobs int) error {
	cache, err := lockfile.Read(output)
	if err != nil {
		return fmt.Errorf("reading existing lockfile: %w", err)
	}
	slog.Info("cache loaded", "mods", len(cache.Mod), "pkgs", len(cache.Pkg))

	allPkgMap := map[string]goListPkg{}
	for _, dir := range dirs {
		slog.Info("collecting packages", "dir", dir)
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
	slog.Info("packages found", "count", len(allPkgMap))

	thirdPartyPkgs := map[string]bool{}
	for ip := range allPkgMap {
		thirdPartyPkgs[ip] = true
	}

	allPkgs := make([]goListPkg, 0, len(allPkgMap))
	for _, pkg := range allPkgMap {
		allPkgs = append(allPkgs, pkg)
	}
	mods := collectModulesFromPackages(allPkgs)
	slog.Info("modules found", "count", len(mods))

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

	var toHash []modInfo
	resultMod := map[string]lockfile.ModEntry{}
	for _, m := range mods {
		if cached, ok := cache.Mod[m.key]; ok && cached.Replaced == m.replaced() {
			cached.NumPkgs = pkgsPerMod[m.key]
			resultMod[m.key] = cached
		} else {
			toHash = append(toHash, m)
		}
	}
	slices.SortFunc(toHash, func(a, b modInfo) int { return strings.Compare(a.key, b.key) })
	slog.Info("hashing", "todo", len(toHash), "cached", len(resultMod))

	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(jobs)
	for _, m := range toHash {
		g.Go(func() error {
			slog.Debug("hashing module", "key", m.key)
			h, err := modCacheHash(m.fetchPath, m.version)
			if err != nil {
				return fmt.Errorf("hashing %s: %w", m.key, err)
			}
			mu.Lock()
			resultMod[m.key] = lockfile.ModEntry{
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

	resultPkg := map[string]lockfile.PkgEntry{}
	for _, pkg := range allPkgs {
		if pkg.Module == nil || pkg.Module.Version == "" {
			continue
		}
		version := pkg.Module.Version
		if r := pkg.Module.Replace; r != nil && r.Version != "" {
			version = r.Version
		}
		modKey := pkg.Module.Path + "@" + version

		var imports []string
		for _, imp := range pkg.Imports {
			if thirdPartyPkgs[imp] {
				imports = append(imports, imp)
			}
		}
		sort.Strings(imports)

		resultPkg[pkg.ImportPath] = lockfile.PkgEntry{
			Module:  modKey,
			Imports: imports,
		}
	}

	result := &lockfile.Lockfile{Mod: resultMod, Pkg: resultPkg}
	slog.Info("writing lockfile", "mods", len(resultMod), "pkgs", len(resultPkg), "path", output)
	return result.Write(output, lockfile.Header)
}

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
		if pkg.Standard {
			continue
		}
		if pkg.Module.isLocal() {
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func collectModulesFromPackages(pkgs []goListPkg) []modInfo {
	seen := map[string]bool{}
	var mods []modInfo
	for _, pkg := range pkgs {
		if pkg.Module == nil || pkg.Module.Version == "" {
			continue
		}
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
		mods = append(mods, modInfo{key: key, fetchPath: fetchPath, version: version})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].key < mods[j].key })
	return mods
}

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

func narHashPath(path string) (string, error) {
	h := sha256.New()
	if err := nar.DumpPath(h, path); err != nil {
		return "", fmt.Errorf("nar hash %s: %w", path, err)
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func removeReadOnly(dir string) {
	filepath.WalkDir(dir, func(path string, _ fs.DirEntry, _ error) error {
		os.Chmod(path, 0o755)
		return nil
	})
	os.RemoveAll(dir)
}
