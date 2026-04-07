// Package localpkgs discovers local Go packages in a module and returns
// them in topological dependency order.
package localpkgs

import (
	"fmt"
	"go/build"
	"io/fs"
	"log/slog"
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
	ImportPath      string            `json:"import_path"`
	SrcDir          string            `json:"src_dir"`
	LocalDeps       []string          `json:"local_deps"` // local-to-local dependency import paths
	TestGoFiles     []string          `json:"test_go_files"`
	XTestGoFiles    []string          `json:"xtest_go_files"`
	TestImports     []string          `json:"test_imports"`
	XTestImports    []string          `json:"xtest_imports"`
	TestEmbedFiles  []string          `json:"test_embed_files"`
	XTestEmbedFiles []string          `json:"xtest_embed_files"`
	TestEmbedCfg    *gofiles.EmbedCfg `json:"test_embed_cfg,omitempty"`
	XTestEmbedCfg   *gofiles.EmbedCfg `json:"xtest_embed_cfg,omitempty"`
	gofiles.PkgFiles
}

// ListLocalPackages discovers all local packages under root (the directory
// containing go.mod) and returns them in topological dependency order.
//
// goVersion is the target Go toolchain version (e.g. "1.25"); see
// gofiles.BuildContext for the meaning. Pass "" to use build.Default.
func ListLocalPackages(root string, tags string, goVersion string) ([]*LocalPkg, error) {
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

	ctx := gofiles.BuildContext(tags, goVersion)
	pkgs := map[string]*LocalPkg{}
	localDeps := map[string][]string{}

	localPrefixes := []string{modulePath}
	for ip := range localReplaces {
		localPrefixes = append(localPrefixes, ip)
	}

	walkDir := func(dir string, importBase string) error {
		return filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				slog.Debug("skipping directory", "path", path, "err", walkErr)
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
				slog.Debug("skipping directory (no Go package)", "path", path, "err", importErr)
				return nil
			}

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return fmt.Errorf("computing relative path for %s: %w", path, err)
			}
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

			// Resolve test-only embed patterns.
			var testEmbedFiles []string
			var testEmbedCfg *gofiles.EmbedCfg
			if len(pkg.TestEmbedPatterns) > 0 {
				cfg, err := gofiles.ResolveEmbedCfg(path, pkg.TestEmbedPatterns)
				if err != nil {
					return fmt.Errorf("resolving test embed patterns for %s: %w", importPath, err)
				}
				testEmbedCfg = cfg
				for f := range cfg.Files {
					testEmbedFiles = append(testEmbedFiles, f)
				}
			}
			var xtestEmbedFiles []string
			var xtestEmbedCfg *gofiles.EmbedCfg
			if len(pkg.XTestEmbedPatterns) > 0 {
				cfg, err := gofiles.ResolveEmbedCfg(path, pkg.XTestEmbedPatterns)
				if err != nil {
					return fmt.Errorf("resolving xtest embed patterns for %s: %w", importPath, err)
				}
				xtestEmbedCfg = cfg
				for f := range cfg.Files {
					xtestEmbedFiles = append(xtestEmbedFiles, f)
				}
			}

			localPkg := &LocalPkg{
				ImportPath:      importPath,
				SrcDir:          path,
				LocalDeps:       local,
				TestGoFiles:     nonNil(pkg.TestGoFiles),
				XTestGoFiles:    nonNil(pkg.XTestGoFiles),
				TestImports:     nonNil(pkg.TestImports),
				XTestImports:    nonNil(pkg.XTestImports),
				TestEmbedFiles:  nonNil(testEmbedFiles),
				XTestEmbedFiles: nonNil(xtestEmbedFiles),
				TestEmbedCfg:    testEmbedCfg,
				XTestEmbedCfg:   xtestEmbedCfg,
				PkgFiles:        pf,
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
