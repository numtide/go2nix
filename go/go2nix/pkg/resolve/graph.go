package resolve

import (
	"strings"

	"github.com/numtide/go2nix/pkg/golist"
	"github.com/numtide/go2nix/pkg/nixdrv"
	"github.com/numtide/go2nix/pkg/toposort"
)

// ResolvedPkg holds a resolved package with all info needed to create a derivation.
type ResolvedPkg struct {
	ImportPath string
	ModKey     string   // "" for local packages
	GoFiles    []string // .go source files (basenames)
	CgoFiles   []string
	CFiles     []string
	CXXFiles   []string
	SFiles     []string
	HFiles     []string
	Imports    []string // all import paths (including stdlib)
	IsLocal    bool
	FodPath    nixdrv.StorePath // FOD output path (third-party only)
	FetchPath  string           // module fetch path (for source lookup within FOD)
	Version    string
	Subdir     string // package path relative to module root
	Name       string // Go package name (e.g., "main", "ssh")

	// Set during derivation creation
	DrvPath nixdrv.StorePath // .drv path after nix derivation add
}

// buildPackageGraph converts go list packages into ResolvedPkgs.
// stdPkgs is the set of standard library import paths to exclude from imports.
// fodPaths maps modKey → materialized FOD StorePath.
func buildPackageGraph(
	pkgs []golist.Pkg,
	fodPaths map[string]nixdrv.StorePath,
) map[string]*ResolvedPkg {
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

		if pkg.Module != nil && !pkg.Module.IsLocal() {
			// Third-party package
			modKey := pkg.Module.ModKey()
			rp.ModKey = modKey
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

			if fp, ok := fodPaths[modKey]; ok {
				rp.FodPath = fp
			}
		} else {
			rp.IsLocal = true
			// Compute subdir for local packages (relative to module root)
			if pkg.Module != nil && pkg.ImportPath != pkg.Module.Path {
				rp.Subdir = strings.TrimPrefix(pkg.ImportPath, pkg.Module.Path+"/")
			}
		}

		result[pkg.ImportPath] = rp
	}
	return result
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

