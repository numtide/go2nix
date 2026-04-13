// Package golist wraps `go list -json -deps` and provides shared types
// for lockfile generation and dynamic derivation resolution.
package golist

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Pkg matches one JSON record from `go list -json -deps`.
type Pkg struct {
	ImportPath     string   `json:"ImportPath"`
	Name           string   `json:"Name"`
	Dir            string   `json:"Dir"`
	GoFiles        []string `json:"GoFiles"`
	CgoFiles       []string `json:"CgoFiles"`
	CFiles         []string `json:"CFiles"`
	CXXFiles       []string `json:"CXXFiles"`
	MFiles         []string `json:"MFiles"` // .m Objective-C source files
	FFiles         []string `json:"FFiles"` // .f, .F, .for, .f90 Fortran source files
	SFiles         []string `json:"SFiles"`
	HFiles         []string `json:"HFiles"`
	SysoFiles      []string `json:"SysoFiles"`     // .syso system object files
	SwigFiles      []string `json:"SwigFiles"`     // .swig files
	SwigCXXFiles   []string `json:"SwigCXXFiles"`  // .swigcxx files
	EmbedPatterns  []string `json:"EmbedPatterns"` // //go:embed patterns
	EmbedFiles     []string `json:"EmbedFiles"`    // resolved files matching embed patterns
	Standard       bool     `json:"Standard"`
	Imports        []string `json:"Imports"`
	DefaultGODEBUG string   `json:"DefaultGODEBUG"` // default GODEBUG for main packages

	Module *Module `json:"Module"`
}

// Module represents module info from `go list -json`.
type Module struct {
	Path      string   `json:"Path"`
	Version   string   `json:"Version"`
	GoVersion string   `json:"GoVersion"` // go directive from go.mod
	Main      bool     `json:"Main"`
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

	args := []string{"list", "-json", "-deps", "-pgo=off", "-mod=readonly"}
	if opts.Tags != "" {
		args = append(args, "-tags", opts.Tags)
	}
	args = append(args, patterns...)

	cmd := exec.Command(gobin, args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("go list stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("go list in %s: %w", opts.Dir, err)
	}

	var pkgs []Pkg
	dec := json.NewDecoder(stdout)
	for dec.More() {
		var pkg Pkg
		if err := dec.Decode(&pkg); err != nil {
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

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("go list in %s: %w", opts.Dir, err)
	}
	injectCgoImports(pkgs)
	return pkgs, nil
}

// injectCgoImports adds the implicit imports that cgo/swig translation
// introduces, matching cmd/go/internal/load.(*Package).resolveInternal
// (src/cmd/go/internal/load/pkg.go:1910-1929).
//
// Without this, we would need `go list -compiled`, which triggers actual
// cgo+gcc compilation for every cgo package (~10-15s overhead). Instead,
// we run plain `go list` (fast) and synthetically inject the same imports.
//
// The Go compiler's cgo tool generates files that import these packages:
//   - "unsafe"      — always, for C pointer conversions
//   - "runtime/cgo" — for cgo runtime support (e.g., C.CString, callbacks)
//   - "syscall"     — for errno handling in cgo calls
//
// The upstream code skips runtime/cgo and syscall for certain stdlib packages
// (cgoExclude/cgoSyscallExclude) to avoid circular dependencies. Since go2nix
// only compiles non-stdlib packages (stdlib is pre-compiled), this exclusion
// does not apply.
func injectCgoImports(pkgs []Pkg) {
	for i := range pkgs {
		if len(pkgs[i].CgoFiles) == 0 && len(pkgs[i].SwigFiles) == 0 && len(pkgs[i].SwigCXXFiles) == 0 {
			continue
		}
		seen := make(map[string]bool, len(pkgs[i].Imports)+3)
		for _, imp := range pkgs[i].Imports {
			seen[imp] = true
		}
		for _, imp := range []string{"unsafe", "runtime/cgo", "syscall"} {
			if !seen[imp] {
				pkgs[i].Imports = append(pkgs[i].Imports, imp)
			}
		}
	}
}

// CollectGoModModules parses go.mod in dir and returns ModInfo for every
// non-local module in the require block. This ensures platform-specific
// and test-only indirect deps (which go list -deps may omit) are included.
func CollectGoModModules(dir string) ([]ModInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("reading go.mod: %w", err)
	}
	mf, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.mod: %w", err)
	}

	// Key by (Old.Path, Old.Version): a replace with Old.Version != "" applies
	// only when the module resolves to exactly that version. The wildcard form
	// (Old.Version == "") applies to any version. Mirrors modfile semantics.
	replaces := make(map[module.Version]*modfile.Replace, len(mf.Replace))
	for _, r := range mf.Replace {
		replaces[r.Old] = r
	}

	seen := map[string]bool{}
	var mods []ModInfo
	for _, req := range mf.Require {
		modPath := req.Mod.Path
		version := req.Mod.Version
		fetchPath := modPath

		r, ok := replaces[req.Mod]
		if !ok {
			r, ok = replaces[module.Version{Path: modPath}]
		}
		if ok {
			if r.New.Version == "" {
				// Local replace — skip.
				continue
			}
			version = r.New.Version
			fetchPath = r.New.Path
		}

		key := module.Version{Path: modPath, Version: version}.String()
		if seen[key] {
			continue
		}
		seen[key] = true

		mods = append(mods, ModInfo{Key: key, FetchPath: fetchPath, Version: version})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Key < mods[j].Key })
	return mods, nil
}
