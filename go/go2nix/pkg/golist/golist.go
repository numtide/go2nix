// Package golist wraps `go list -json -deps` and provides shared types
// for lockfile generation and dynamic derivation resolution.
package golist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"golang.org/x/mod/module"
)

// Pkg matches one JSON record from `go list -json -deps`.
type Pkg struct {
	ImportPath string   `json:"ImportPath"`
	Name       string   `json:"Name"`
	Dir        string   `json:"Dir"`
	GoFiles    []string `json:"GoFiles"`
	CgoFiles   []string `json:"CgoFiles"`
	CFiles     []string `json:"CFiles"`
	CXXFiles   []string `json:"CXXFiles"`
	FFiles     []string `json:"FFiles"` // .f, .F, .for, .f90 Fortran source files
	SFiles     []string `json:"SFiles"`
	HFiles     []string `json:"HFiles"`
	Standard   bool     `json:"Standard"`
	DepOnly    bool     `json:"DepOnly"`
	Imports    []string `json:"Imports"`

	Module *Module `json:"Module"`
}

// Module represents module info from `go list -json`.
type Module struct {
	Path      string   `json:"Path"`
	Version   string   `json:"Version"`
	GoVersion string   `json:"GoVersion"` // go directive from go.mod
	Main      bool     `json:"Main"`
	Dir       string   `json:"Dir"`
	Replace   *Replace `json:"Replace"`
}

// Replace represents a module replacement.
type Replace struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

// IsLocal returns true if this module is the main module or a local replace.
func (m *Module) IsLocal() bool {
	if m == nil || m.Main {
		return true
	}
	if m.Replace != nil && m.Replace.Version == "" {
		return true
	}
	return false
}

// ModKey returns "path@version" for this module.
// Uses the original module path with the effective version (replacement if any).
func (m *Module) ModKey() string {
	version := m.Version
	if r := m.Replace; r != nil && r.Version != "" {
		version = r.Version
	}
	return module.Version{Path: m.Path, Version: version}.String()
}

// FetchPath returns the actual path to fetch (respects replaces).
func (m *Module) FetchPath() string {
	if r := m.Replace; r != nil && r.Version != "" {
		return r.Path
	}
	return m.Path
}

// ModInfo holds deduplicated module information.
type ModInfo struct {
	Key       string // path@version
	FetchPath string // actual path to fetch
	Version   string
}

// Replaced returns the replacement path if this module is replaced, empty string otherwise.
func (m ModInfo) Replaced() string {
	origPath := strings.TrimSuffix(m.Key, "@"+m.Version)
	if origPath != m.FetchPath {
		return m.FetchPath
	}
	return ""
}

// ListDepsOptions configures ListDeps behavior.
type ListDepsOptions struct {
	Dir       string   // working directory
	GoBin     string   // path to go binary (default: "go")
	Env       []string // extra environment variables
	Tags      string   // build tags (comma-separated)
	Patterns  []string // patterns to list (default: ["./..."])
	KeepLocal bool     // if true, include local packages in results
	Compiled  bool     // if true, pass -compiled to include cgo-generated imports in Imports
}

// ListDeps runs `go list -json -deps` and returns packages.
func ListDeps(opts ListDepsOptions) ([]Pkg, error) {
	gobin := opts.GoBin
	if gobin == "" {
		gobin = "go"
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	args := []string{"list", "-json", "-deps"}
	if opts.Compiled {
		args = append(args, "-compiled")
	}
	if opts.Tags != "" {
		args = append(args, "-tags", opts.Tags)
	}
	args = append(args, patterns...)

	cmd := exec.Command(gobin, args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w", opts.Dir, err)
	}

	var pkgs []Pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg Pkg
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		if pkg.Standard {
			continue
		}
		if !opts.KeepLocal && pkg.Module.IsLocal() {
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// CollectModules deduplicates modules from a list of packages.
func CollectModules(pkgs []Pkg) []ModInfo {
	seen := map[string]bool{}
	var mods []ModInfo
	for _, pkg := range pkgs {
		if pkg.Module == nil || pkg.Module.Version == "" {
			continue
		}
		if pkg.Module.IsLocal() {
			continue
		}
		key := pkg.Module.ModKey()
		if seen[key] {
			continue
		}
		seen[key] = true

		fetchPath := pkg.Module.FetchPath()
		version := pkg.Module.Version
		if r := pkg.Module.Replace; r != nil && r.Version != "" {
			version = r.Version
		}

		mods = append(mods, ModInfo{Key: key, FetchPath: fetchPath, Version: version})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Key < mods[j].Key })
	return mods
}
