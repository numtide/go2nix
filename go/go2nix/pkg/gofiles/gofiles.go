// Package gofiles discovers Go source files in a package directory,
// resolving build constraints and embed patterns.
package gofiles

import (
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PkgFiles describes the files in a Go package, resolved for the current
// platform's build constraints.
type PkgFiles struct {
	GoFiles    []string  `json:"go_files"`
	CgoFiles   []string  `json:"cgo_files"`
	SFiles     []string  `json:"s_files"`
	CFiles     []string  `json:"c_files"`
	CXXFiles   []string  `json:"cxx_files"`
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
func ResolveEmbedCfg(dir string, patterns []string) (*EmbedCfg, error) {
	cfg := &EmbedCfg{
		Patterns: make(map[string][]string),
		Files:    make(map[string]string),
	}

	for _, pattern := range patterns {
		includeHidden := false
		matchPattern := pattern
		if strings.HasPrefix(pattern, "all:") {
			includeHidden = true
			matchPattern = strings.TrimPrefix(pattern, "all:")
		}

		var matched []string

		info, err := os.Stat(filepath.Join(dir, matchPattern))
		if err == nil && info.IsDir() {
			err := filepath.WalkDir(filepath.Join(dir, matchPattern), func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(dir, path)
				name := d.Name()
				if !includeHidden && strings.HasPrefix(name, ".") {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if !includeHidden && strings.HasPrefix(name, "_") {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if !d.IsDir() {
					matched = append(matched, rel)
					cfg.Files[rel] = filepath.Join(dir, rel)
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("walking %s for pattern %q: %w", matchPattern, pattern, err)
			}
		} else {
			matches, err := filepath.Glob(filepath.Join(dir, matchPattern))
			if err != nil {
				return nil, fmt.Errorf("glob %q: %w", pattern, err)
			}
			patBase := filepath.Base(matchPattern)
			patAllowsDot := strings.HasPrefix(patBase, ".")
			patAllowsUnderscore := strings.HasPrefix(patBase, "_")
			for _, m := range matches {
				rel, _ := filepath.Rel(dir, m)
				name := filepath.Base(rel)
				if !includeHidden && !patAllowsDot && strings.HasPrefix(name, ".") {
					continue
				}
				if !includeHidden && !patAllowsUnderscore && strings.HasPrefix(name, "_") {
					continue
				}
				fi, err := os.Stat(m)
				if err != nil || fi.IsDir() {
					continue
				}
				matched = append(matched, rel)
				cfg.Files[rel] = filepath.Join(dir, rel)
			}
		}

		cfg.Patterns[pattern] = matched
	}

	return cfg, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
