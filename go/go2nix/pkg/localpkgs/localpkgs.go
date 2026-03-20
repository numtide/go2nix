// Package localpkgs discovers local Go packages in a module and returns
// them in topological dependency order.
package localpkgs

import (
	"fmt"
	"go/build"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/numtide/go2nix/pkg/gofiles"
	"github.com/numtide/go2nix/pkg/toposort"
	"golang.org/x/mod/modfile"
)

// LocalPkg describes a local package with its files and location.
type LocalPkg struct {
	ImportPath   string   `json:"import_path"`
	SrcDir       string   `json:"src_dir"`
	LocalDeps    []string `json:"local_deps"` // local-to-local dependency import paths
	TestGoFiles  []string `json:"test_go_files"`
	XTestGoFiles []string `json:"xtest_go_files"`
	TestImports  []string `json:"test_imports"`
	XTestImports []string `json:"xtest_imports"`
	gofiles.PkgFiles
}

// ListLocalPackages discovers all local packages under root (the directory
// containing go.mod) and returns them in topological dependency order.
func ListLocalPackages(root string, tags string) ([]*LocalPkg, error) {
	goModData, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("reading go.mod: %w", err)
	}
	modFile, err := modfile.Parse("go.mod", goModData, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.mod: %w", err)
	}
	modulePath := modFile.Module.Mod.Path

	localReplaces := map[string]string{}
	for _, rep := range modFile.Replace {
		if rep.New.Version == "" {
			absDir := rep.New.Path
			if !filepath.IsAbs(absDir) {
				absDir = filepath.Join(root, absDir)
			}
			absDir, _ = filepath.Abs(absDir)
			localReplaces[rep.Old.Path] = absDir
		}
	}

	ctx := gofiles.BuildContext(tags)
	pkgs := map[string]*LocalPkg{}
	localDeps := map[string][]string{}

	localPrefixes := []string{modulePath}
	for ip := range localReplaces {
		localPrefixes = append(localPrefixes, ip)
	}

	walkDir := func(dir string, importBase string) error {
		return filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == "vendor" || name == "testdata" || name == ".git" || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}

			pkg, importErr := ctx.ImportDir(path, build.IgnoreVendor)
			if importErr != nil {
				return nil
			}

			rel, _ := filepath.Rel(dir, path)
			importPath := importBase
			if rel != "." {
				importPath = importBase + "/" + rel
			}

			pf, err := gofiles.BuildPkgFiles(pkg, path)
			if err != nil {
				return fmt.Errorf("building pkg files for %s: %w", importPath, err)
			}

			var local []string
			for _, imp := range pkg.Imports {
				if isLocalImport(imp, localPrefixes) {
					local = append(local, imp)
				}
			}

			localPkg := &LocalPkg{
				ImportPath:   importPath,
				SrcDir:       path,
				LocalDeps:    local,
				TestGoFiles:  nonNil(pkg.TestGoFiles),
				XTestGoFiles: nonNil(pkg.XTestGoFiles),
				TestImports:  nonNil(pkg.TestImports),
				XTestImports: nonNil(pkg.XTestImports),
				PkgFiles:     pf,
			}
			pkgs[importPath] = localPkg
			localDeps[importPath] = local
			return nil
		})
	}

	if err := walkDir(root, modulePath); err != nil {
		return nil, err
	}

	for _, importPath := range slices.Sorted(maps.Keys(localReplaces)) {
		dir := localReplaces[importPath]
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return nil, fmt.Errorf("local replace target %s (%s) does not exist", importPath, dir)
		}
		if err := walkDir(dir, importPath); err != nil {
			return nil, err
		}
	}

	return toposort.Sort(pkgs, func(key string) []string {
		return localDeps[key]
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func isLocalImport(imp string, prefixes []string) bool {
	for _, p := range prefixes {
		if imp == p || strings.HasPrefix(imp, p+"/") {
			return true
		}
	}
	return false
}
