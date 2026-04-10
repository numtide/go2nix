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
//
// ListLocalPackages populates the *Patterns fields with the raw //go:embed
// patterns from go/build but does NOT resolve them. Call ResolveEmbeds to
// populate the *Cfg/*Files fields. This split lets the testrunner skip
// out-of-scope packages whose embed targets are absent from the source
// closure (e.g. supplied at build time via srcOverlay) without failing the
// listing step.
type LocalPkg struct {
	ImportPath         string            `json:"import_path"`
	SrcDir             string            `json:"src_dir"`
	LocalDeps          []string          `json:"local_deps"` // local-to-local dependency import paths
	TestGoFiles        []string          `json:"test_go_files"`
	XTestGoFiles       []string          `json:"xtest_go_files"`
	TestImports        []string          `json:"test_imports"`
	XTestImports       []string          `json:"xtest_imports"`
	EmbedPatterns      []string          `json:"embed_patterns,omitempty"`
	TestEmbedPatterns  []string          `json:"test_embed_patterns,omitempty"`
	XTestEmbedPatterns []string          `json:"xtest_embed_patterns,omitempty"`
	TestEmbedFiles     []string          `json:"test_embed_files"`
	XTestEmbedFiles    []string          `json:"xtest_embed_files"`
	TestEmbedCfg       *gofiles.EmbedCfg `json:"test_embed_cfg,omitempty"`
	XTestEmbedCfg      *gofiles.EmbedCfg `json:"xtest_embed_cfg,omitempty"`
	gofiles.PkgFiles
}

// ResolveEmbeds resolves the package's production, test, and xtest //go:embed
// patterns against SrcDir, populating EmbedCfg/EmbedFiles, TestEmbedCfg/
// TestEmbedFiles and XTestEmbedCfg/XTestEmbedFiles.
func (p *LocalPkg) ResolveEmbeds() error {
	resolve := func(patterns []string) (*gofiles.EmbedCfg, []string, error) {
		if len(patterns) == 0 {
			return nil, []string{}, nil
		}
		cfg, err := gofiles.ResolveEmbedCfg(p.SrcDir, patterns)
		if err != nil {
			return nil, nil, err
		}
		files := slices.Sorted(maps.Keys(cfg.Files))
		return cfg, files, nil
	}

	cfg, files, err := resolve(p.EmbedPatterns)
	if err != nil {
		return fmt.Errorf("resolving embed patterns for %s: %w", p.ImportPath, err)
	}
	p.EmbedCfg, p.EmbedFiles = cfg, files

	cfg, files, err = resolve(p.TestEmbedPatterns)
	if err != nil {
		return fmt.Errorf("resolving test embed patterns for %s: %w", p.ImportPath, err)
	}
	p.TestEmbedCfg, p.TestEmbedFiles = cfg, files

	cfg, files, err = resolve(p.XTestEmbedPatterns)
	if err != nil {
		return fmt.Errorf("resolving xtest embed patterns for %s: %w", p.ImportPath, err)
	}
	p.XTestEmbedCfg, p.XTestEmbedFiles = cfg, files

	return nil
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
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if path != dir {
				// Match cmd/go's `./...` semantics: skip all dot-directories
				// (.git, .idea, .direnv, ...) and stop at nested module
				// boundaries.
				if strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
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

			var local []string
			for _, imp := range pkg.Imports {
				if isLocalImport(imp, localPrefixes) {
					local = append(local, imp)
				}
			}

			localPkg := &LocalPkg{
				ImportPath:         importPath,
				SrcDir:             path,
				LocalDeps:          local,
				TestGoFiles:        nonNil(pkg.TestGoFiles),
				XTestGoFiles:       nonNil(pkg.XTestGoFiles),
				TestImports:        nonNil(pkg.TestImports),
				XTestImports:       nonNil(pkg.XTestImports),
				EmbedPatterns:      pkg.EmbedPatterns,
				TestEmbedPatterns:  pkg.TestEmbedPatterns,
				XTestEmbedPatterns: pkg.XTestEmbedPatterns,
				TestEmbedFiles:     []string{},
				XTestEmbedFiles:    []string{},
				PkgFiles: gofiles.PkgFiles{
					GoFiles:      nonNil(pkg.GoFiles),
					CgoFiles:     nonNil(pkg.CgoFiles),
					SFiles:       nonNil(pkg.SFiles),
					CFiles:       nonNil(pkg.CFiles),
					CXXFiles:     nonNil(pkg.CXXFiles),
					MFiles:       nonNil(pkg.MFiles),
					FFiles:       nonNil(pkg.FFiles),
					HFiles:       nonNil(pkg.HFiles),
					SysoFiles:    nonNil(pkg.SysoFiles),
					SwigFiles:    nonNil(pkg.SwigFiles),
					SwigCXXFiles: nonNil(pkg.SwigCXXFiles),
					EmbedFiles:   []string{},
					IsCommand:    pkg.IsCommand(),
				},
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
