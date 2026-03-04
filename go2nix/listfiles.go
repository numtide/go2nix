package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/build"
	"io/fs"
	"log"
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

// buildPkgFiles constructs a PkgFiles from a go/build.Package, resolving
// embed patterns if present. Shared by list-files and list-local-packages.
func buildPkgFiles(pkg *build.Package, dir string) PkgFiles {
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
		cfg, err := resolveEmbedCfg(dir, pkg.EmbedPatterns)
		if err != nil {
			log.Fatalf("resolving embed patterns in %s: %v", dir, err)
		}
		result.EmbedCfg = cfg
	}

	return result
}

// buildContext returns a build.Context configured from flags and environment.
func buildContext(tags string) build.Context {
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

func listFilesCmd(args []string) {
	flagSet := flag.NewFlagSet("list-files", flag.ExitOnError)
	tagsFlag := flagSet.String("tags", "", "comma-separated build tags")
	flagSet.Parse(args)
	if flagSet.NArg() != 1 {
		log.Fatal("usage: go2nix list-files [-tags=...] <package-dir>")
	}
	dir := flagSet.Arg(0)

	ctx := buildContext(*tagsFlag)

	pkg, err := ctx.ImportDir(dir, build.IgnoreVendor)
	if err != nil {
		log.Fatalf("analysing %s: %v", dir, err)
	}

	result := buildPkgFiles(pkg, dir)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "encoding JSON: %v\n", err)
		os.Exit(1)
	}
}

// resolveEmbedCfg resolves embed patterns to file paths relative to dir,
// producing the JSON structure expected by go tool compile -embedcfg.
func resolveEmbedCfg(dir string, patterns []string) (*EmbedCfg, error) {
	cfg := &EmbedCfg{
		Patterns: make(map[string][]string),
		Files:    make(map[string]string),
	}

	for _, pattern := range patterns {
		// Handle "all:" prefix (includes hidden files/dirs).
		includeHidden := false
		matchPattern := pattern
		if strings.HasPrefix(pattern, "all:") {
			includeHidden = true
			matchPattern = strings.TrimPrefix(pattern, "all:")
		}

		var matched []string

		// Check if this is a directory embed (pattern is a bare directory name).
		info, err := os.Stat(filepath.Join(dir, matchPattern))
		if err == nil && info.IsDir() {
			// Walk the directory tree.
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
			// Glob pattern.
			matches, err := filepath.Glob(filepath.Join(dir, matchPattern))
			if err != nil {
				return nil, fmt.Errorf("glob %q: %w", pattern, err)
			}
			// Go's embed rule: files starting with "." or "_" are only
			// excluded when the pattern itself doesn't start with "." or "_".
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
				// Skip directories for glob matches.
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
