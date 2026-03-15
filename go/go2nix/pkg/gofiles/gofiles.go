// Package gofiles discovers Go source files in a package directory,
// resolving build constraints and embed patterns.
package gofiles

import (
	"fmt"
	"go/build"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/module"
)

// PkgFiles describes the files in a Go package, resolved for the current
// platform's build constraints.
type PkgFiles struct {
	GoFiles    []string  `json:"go_files"`
	CgoFiles   []string  `json:"cgo_files"`
	SFiles     []string  `json:"s_files"`
	CFiles     []string  `json:"c_files"`
	CXXFiles   []string  `json:"cxx_files"`
	FFiles     []string  `json:"f_files"` // .f, .F, .for, .f90 Fortran source files
	HFiles     []string  `json:"h_files"`
	EmbedFiles []string  `json:"embed_files"`
	EmbedCfg   *EmbedCfg `json:"embed_cfg,omitempty"`
	IsCommand  bool      `json:"is_command"`
}

// EmbedCfg is the format expected by go tool compile -embedcfg.
type EmbedCfg struct {
	Patterns map[string][]string `json:"Patterns"`
	Files    map[string]string   `json:"Files"`
}

// BuildContext returns a build.Context configured from tags and environment.
func BuildContext(tags string) build.Context {
	ctx := build.Default
	if tags != "" {
		ctx.BuildTags = strings.Split(tags, ",")
	}
	if v := os.Getenv("GOOS"); v != "" {
		ctx.GOOS = v
	}
	if v := os.Getenv("GOARCH"); v != "" {
		ctx.GOARCH = v
	}
	return ctx
}

// BuildPkgFiles constructs a PkgFiles from a go/build.Package, resolving
// embed patterns if present.
func BuildPkgFiles(pkg *build.Package, dir string) (PkgFiles, error) {
	result := PkgFiles{
		GoFiles:    nonNil(pkg.GoFiles),
		CgoFiles:   nonNil(pkg.CgoFiles),
		SFiles:     nonNil(pkg.SFiles),
		CFiles:     nonNil(pkg.CFiles),
		CXXFiles:   nonNil(pkg.CXXFiles),
		FFiles:     nonNil(pkg.FFiles),
		HFiles:     nonNil(pkg.HFiles),
		EmbedFiles: nonNil(pkg.EmbedPatterns),
		IsCommand:  pkg.IsCommand(),
	}

	if len(pkg.EmbedPatterns) > 0 {
		cfg, err := ResolveEmbedCfg(dir, pkg.EmbedPatterns)
		if err != nil {
			return PkgFiles{}, fmt.Errorf("resolving embed patterns in %s: %w", dir, err)
		}
		result.EmbedCfg = cfg
	}

	return result, nil
}

// ListFiles discovers source files in dir using the given build tags.
func ListFiles(dir string, tags string) (PkgFiles, error) {
	ctx := BuildContext(tags)
	pkg, err := ctx.ImportDir(dir, build.IgnoreVendor)
	if err != nil {
		return PkgFiles{}, fmt.Errorf("analysing %s: %w", dir, err)
	}
	return BuildPkgFiles(pkg, dir)
}

// ResolveEmbedCfg resolves embed patterns to file paths relative to dir,
// producing the JSON structure expected by go tool compile -embedcfg.
//
// Ported from cmd/go/internal/load.resolveEmbed, using os/filepath instead
// of the internal fsys/str packages. We omit the GODEBUG embedfollowsymlinks
// knob since it's a niche opt-in that doesn't affect Nix sandbox builds.
func ResolveEmbedCfg(dir string, patterns []string) (*EmbedCfg, error) {
	cfg := &EmbedCfg{
		Patterns: make(map[string][]string),
		Files:    make(map[string]string),
	}

	have := make(map[string]int)
	dirOK := make(map[string]bool)
	pid := 0

	for _, pattern := range patterns {
		pid++
		glob, all := strings.CutPrefix(pattern, "all:")

		if _, err := pathpkg.Match(glob, ""); err != nil || !validEmbedPattern(glob) {
			return nil, fmt.Errorf("pattern %q: invalid pattern syntax", pattern)
		}

		match, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(glob)))
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", pattern, err)
		}

		var list []string
		for _, file := range match {
			rel, _ := filepath.Rel(dir, file)
			rel = filepath.ToSlash(rel)

			info, err := os.Lstat(file)
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", pattern, err)
			}

			what := "file"
			if info.IsDir() {
				what = "directory"
			}

			// Check parent directories for module boundaries and bad names.
			for d := file; len(d) > len(dir)+1 && !dirOK[d]; d = filepath.Dir(d) {
				if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
					return nil, fmt.Errorf("cannot embed %s %s: in different module", what, rel)
				}
				if d != file {
					if di, err := os.Lstat(d); err == nil && !di.IsDir() {
						return nil, fmt.Errorf("cannot embed %s %s: in non-directory %s",
							what, rel, d[len(dir)+1:])
					}
				}
				dirOK[d] = true
				if elem := filepath.Base(d); isBadEmbedName(elem) {
					if d == file {
						return nil, fmt.Errorf("cannot embed %s %s: invalid name %s", what, rel, elem)
					}
					return nil, fmt.Errorf("cannot embed %s %s: in invalid directory %s", what, rel, elem)
				}
			}

			switch {
			case info.Mode().IsRegular():
				if have[rel] != pid {
					have[rel] = pid
					list = append(list, rel)
				}

			case info.IsDir():
				count := 0
				err := filepath.WalkDir(file, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					rel := filepath.ToSlash(mustRel(dir, path))
					name := d.Name()

					if path != file && (isBadEmbedName(name) || ((name[0] == '.' || name[0] == '_') && !all)) {
						if d.IsDir() {
							return fs.SkipDir
						}
						if name[0] == '.' || name[0] == '_' {
							return nil
						}
						if isBadEmbedName(name) {
							return fmt.Errorf("cannot embed file %s: invalid name %s", rel, name)
						}
						return nil
					}
					if d.IsDir() {
						if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
							return filepath.SkipDir
						}
						return nil
					}
					if !d.Type().IsRegular() {
						return nil
					}
					count++
					if have[rel] != pid {
						have[rel] = pid
						list = append(list, rel)
					}
					return nil
				})
				if err != nil {
					return nil, fmt.Errorf("pattern %q: %w", pattern, err)
				}
				if count == 0 {
					return nil, fmt.Errorf("cannot embed directory %s: contains no embeddable files", rel)
				}

			default:
				return nil, fmt.Errorf("cannot embed irregular file %s", rel)
			}
		}

		if len(list) == 0 {
			return nil, fmt.Errorf("pattern %q: no matching files found", pattern)
		}
		sort.Strings(list)
		cfg.Patterns[pattern] = list
	}

	for file := range have {
		cfg.Files[file] = filepath.Join(dir, filepath.FromSlash(file))
	}

	return cfg, nil
}

// validEmbedPattern reports whether pattern is a valid //go:embed pattern.
func validEmbedPattern(pattern string) bool {
	return pattern != "." && fs.ValidPath(pattern)
}

// isBadEmbedName reports whether name is a file/directory name that can't
// or won't be included in modules and therefore shouldn't be embedded.
func isBadEmbedName(name string) bool {
	if err := module.CheckFilePath(name); err != nil {
		return true
	}
	switch name {
	case "", ".bzr", ".hg", ".git", ".svn":
		return true
	}
	return false
}

func mustRel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		panic(err)
	}
	return rel
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
