package resolve

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nix-community/go-nix/pkg/storepath"
	"github.com/numtide/go2nix/pkg/golist"
	"github.com/numtide/go2nix/pkg/toposort"
)

// ResolvedPkg holds a resolved package with all info needed to create a derivation.
type ResolvedPkg struct {
	ImportPath string
	ModKey     string   // "" for local packages (path@version)
	ModPath    string   // original module path (before replace), "" for local
	GoFiles    []string // .go source files (basenames)
	CgoFiles   []string
	CFiles     []string
	CXXFiles   []string
	SFiles     []string
	HFiles     []string
	Imports    []string // all import paths (including stdlib)
	IsLocal    bool
	FodPath    *storepath.StorePath // FOD output path (third-party only)
	FetchPath  string               // module fetch path (for source lookup within FOD)
	Version    string
	Subdir     string // package path relative to module root
	Name       string // Go package name (e.g., "main", "ssh")
	GoVersion  string // Go language version from module's go.mod (e.g., "1.21")

	// Set during derivation creation
	DrvPath *storepath.StorePath // .drv path after nix derivation add
}

// buildPackageGraph converts go list packages into ResolvedPkgs.
// srcRoot is the source store path for computing local package subdirectories.
// fodPaths maps modKey → materialized FOD StorePath.
// Returns an error if a third-party module is missing from fodPaths (stale lockfile).
func buildPackageGraph(
	pkgs []golist.Pkg,
	fodPaths map[string]*storepath.StorePath,
	srcRoot string,
) (map[string]*ResolvedPkg, error) {
	result := make(map[string]*ResolvedPkg, len(pkgs))
	for _, pkg := range pkgs {
		if pkg.Standard {
			continue
		}

		rp := &ResolvedPkg{
			ImportPath: pkg.ImportPath,
			GoFiles:    pkg.GoFiles,
			CgoFiles:   pkg.CgoFiles,
			CFiles:     pkg.CFiles,
			CXXFiles:   pkg.CXXFiles,
			SFiles:     pkg.SFiles,
			HFiles:     pkg.HFiles,
			Name:       pkg.Name,
			Imports:    pkg.Imports, // keep ALL imports (consumers check graph for stdlib vs non-stdlib)
		}
		if pkg.Module != nil {
			rp.GoVersion = pkg.Module.GoVersion
		}

		if pkg.Module != nil && !pkg.Module.IsLocal() {
			// Third-party package
			rp.ModKey = pkg.Module.ModKey()
			rp.ModPath = pkg.Module.Path
			rp.FetchPath = pkg.Module.FetchPath()
			rp.Version = pkg.Module.Version
			if r := pkg.Module.Replace; r != nil && r.Version != "" {
				rp.Version = r.Version
			}

			// Compute subdir: package path relative to module root
			modPath := pkg.Module.Path
			if pkg.ImportPath == modPath {
				rp.Subdir = ""
			} else {
				rp.Subdir = strings.TrimPrefix(pkg.ImportPath, modPath+"/")
			}

			fp, ok := fodPaths[rp.ModKey]
			if !ok {
				return nil, fmt.Errorf("lockfile missing module %s — regenerate with go2nix generate", rp.ModKey)
			}
			rp.FodPath = fp
		} else {
			rp.IsLocal = true
			// Compute subdir for local packages relative to source root.
			// Use pkg.Dir from go list (the actual filesystem path) so that
			// local replace directives (e.g., replace foo => ./libs/foo)
			// resolve to the correct subdirectory.
			if pkg.Dir != "" && srcRoot != "" {
				if rel, err := filepath.Rel(srcRoot, pkg.Dir); err == nil {
					rp.Subdir = rel
				}
			} else if pkg.Module != nil && pkg.ImportPath != pkg.Module.Path {
				// Fallback when Dir is not available
				rp.Subdir = strings.TrimPrefix(pkg.ImportPath, pkg.Module.Path+"/")
			}
		}

		result[pkg.ImportPath] = rp
	}
	return result, nil
}

// topoSort returns packages in dependency order (leaves first).
// Returns an error if cycles are detected.
func topoSort(pkgs map[string]*ResolvedPkg) ([]*ResolvedPkg, error) {
	return toposort.Sort(pkgs, func(key string) []string {
		if pkg, ok := pkgs[key]; ok {
			return pkg.Imports
		}
		return nil
	})
}
