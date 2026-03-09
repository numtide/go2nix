// Package resolve implements the dynamic derivation resolve flow.
// It runs inside a recursive-nix wrapper derivation at build time,
// creating Nix derivations for each Go package via `nix derivation add`.
package resolve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/numtide/go2nix/pkg/golist"
	"github.com/numtide/go2nix/pkg/lockfile"
	"github.com/numtide/go2nix/pkg/nixdrv"
)

// Config holds all configuration for the resolve flow.
type Config struct {
	Src         string // store path to source
	LockFile    string // path to go2nix.toml lockfile
	System      string // e.g., "x86_64-linux"
	GoBin       string // path to go binary
	StdlibPath  string // path to pre-compiled stdlib
	NixBin      string // path to nix binary
	Go2NixBin   string // path to go2nix binary
	BashBin     string // path to bash binary
	PName       string // output binary name
	SubPackages string // comma-separated sub-packages (default "./...")
	Tags        string // comma-separated build tags
	LDFlags     string // linker flags
	CGOEnabled  string // "0" or "1" to override cgo detection, empty for auto
	GCFlags     string // extra flags for go tool compile
	Overrides   string // JSON-encoded packageOverrides
	CACert      string // path to CA certificate bundle
	NetrcFile   string // path to .netrc file for private module authentication
	Output      string // $out path
}

// PackageOverride holds per-package overrides from Nix eval time.
type PackageOverride struct {
	NativeBuildInputs []string `json:"nativeBuildInputs"`
}

// Resolve orchestrates the full dynamic derivation resolve flow.
func Resolve(cfg Config) error {
	nix := &nixdrv.NixTool{
		NixBin: cfg.NixBin,
		ExtraArgs: []string{
			"--extra-experimental-features", "nix-command ca-derivations dynamic-derivations",
		},
	}

	// Parse overrides
	overrides := map[string]PackageOverride{}
	if cfg.Overrides != "" && cfg.Overrides != "{}" {
		if err := json.Unmarshal([]byte(cfg.Overrides), &overrides); err != nil {
			return fmt.Errorf("parsing overrides: %w", err)
		}
	}

	// Step 1: Read lockfile
	slog.Info("reading lockfile", "path", cfg.LockFile)
	lock, err := lockfile.Read(cfg.LockFile)
	if err != nil {
		return fmt.Errorf("reading lockfile: %w", err)
	}
	slog.Info("lockfile loaded", "modules", len(lock.Mod))

	// Step 2: Create module FODs
	slog.Info("creating module FODs")
	fodDrvPaths, err := createModuleFODs(cfg, nix, lock)
	if err != nil {
		return err
	}

	// Step 3: Materialize FODs (build them)
	slog.Info("building module FODs", "count", len(fodDrvPaths))
	fodPaths, err := buildFODs(nix, fodDrvPaths)
	if err != nil {
		return err
	}

	// Step 4: Set up GOMODCACHE
	gomodcache, err := setupGOMODCACHE(fodPaths)
	if err != nil {
		return fmt.Errorf("setting up GOMODCACHE: %w", err)
	}
	defer os.RemoveAll(gomodcache)

	// Step 5: Discover packages
	slog.Info("discovering packages")
	subPkgs := []string{"./..."}
	if cfg.SubPackages != "" {
		subPkgs = strings.Split(cfg.SubPackages, ",")
	}

	golistEnv := []string{
		"GOMODCACHE=" + gomodcache,
		"GONOSUMCHECK=*",
		"GOPROXY=off",
		"GOFLAGS=-mod=mod",
	}
	if cfg.CGOEnabled != "" {
		golistEnv = append(golistEnv, "CGO_ENABLED="+cfg.CGOEnabled)
	}

	pkgs, err := golist.ListDeps(golist.ListDepsOptions{
		Dir:       cfg.Src,
		GoBin:     cfg.GoBin,
		Tags:      cfg.Tags,
		Patterns:  subPkgs,
		Env:       golistEnv,
		KeepLocal: true,
	})
	if err != nil {
		return fmt.Errorf("discovering packages: %w", err)
	}
	slog.Info("packages discovered", "count", len(pkgs))

	// Step 6: Build and topo-sort package graph
	graph := buildPackageGraph(pkgs, fodPaths)
	sorted, err := topoSort(graph)
	if err != nil {
		return fmt.Errorf("topological sort: %w", err)
	}
	slog.Info("packages sorted", "count", len(sorted))

	// Step 7: Create package CA derivations (in topo order)
	slog.Info("creating package derivations")
	for _, pkg := range sorted {
		if err := createPackageDrv(cfg, nix, graph, pkg, overrides); err != nil {
			return fmt.Errorf("creating derivation for %s: %w", pkg.ImportPath, err)
		}
	}

	// Step 8+9: Create link/collector derivation
	slog.Info("creating link derivation")
	finalDrvPath, err := createFinalDrv(cfg, nix, graph, sorted)
	if err != nil {
		return err
	}

	// Step 10: Copy .drv file to $out
	slog.Info("writing output", "drv", finalDrvPath.String(), "out", cfg.Output)
	if err := copyFile(finalDrvPath.String(), cfg.Output); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// createModuleFODs creates a FOD derivation for each module in the lockfile.
// Returns modKey → .drv StorePath.
func createModuleFODs(cfg Config, nix *nixdrv.NixTool, lock *lockfile.Lockfile) (map[string]nixdrv.StorePath, error) {
	result := make(map[string]nixdrv.StorePath, len(lock.Mod))
	bashStorePath := storeDirOf(cfg.BashBin)

	for modKey, hash := range lock.Mod {
		parts := strings.SplitN(modKey, "@", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid module key: %s", modKey)
		}
		modPath, version := parts[0], parts[1]

		fetchPath := modPath
		if r, ok := lock.Replace[modKey]; ok {
			fetchPath = r
		}

		drvName := nixdrv.ModDrvName(modKey)
		script := fodScript(cfg.GoBin, fetchPath, version, cfg.CACert, cfg.NetrcFile)

		drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
		drv.AddArg("-c")
		drv.AddArg(script)
		drv.AddFODOutput("out", "sha256", "nar", hash)

		// Set env.out to standard placeholder
		drv.SetEnv("out", nixdrv.StandardOutput("out").Render())

		// Input sources: go binary, cacert, netrc
		goStoreDir := storeDirOf(cfg.GoBin)
		drv.AddInputSrc(goStoreDir)
		if cfg.CACert != "" {
			drv.AddInputSrc(storeDirOf(cfg.CACert))
		}
		if cfg.NetrcFile != "" {
			drv.AddInputSrc(storeDirOf(cfg.NetrcFile))
		}

		drvPath, err := nix.DerivationAdd(drv)
		if err != nil {
			return nil, fmt.Errorf("creating FOD for %s: %w", modKey, err)
		}
		result[modKey] = drvPath
	}
	return result, nil
}

// buildFODs materializes all FODs in a single batched nix build call.
// Returns modKey → output StorePath.
func buildFODs(nix *nixdrv.NixTool, fodDrvPaths map[string]nixdrv.StorePath) (map[string]nixdrv.StorePath, error) {
	if len(fodDrvPaths) == 0 {
		return map[string]nixdrv.StorePath{}, nil
	}

	// Build a deterministic ordered list of modKeys and installables
	modKeys := make([]string, 0, len(fodDrvPaths))
	for k := range fodDrvPaths {
		modKeys = append(modKeys, k)
	}
	sort.Strings(modKeys)

	installables := make([]string, len(modKeys))
	for i, k := range modKeys {
		installables[i] = fodDrvPaths[k].String() + "^out"
	}

	// Single nix build call — Nix handles parallelism internally
	paths, err := nix.Build(installables...)
	if err != nil {
		return nil, fmt.Errorf("building FODs: %w", err)
	}
	if len(paths) != len(modKeys) {
		return nil, fmt.Errorf("expected %d FOD outputs, got %d", len(modKeys), len(paths))
	}

	result := make(map[string]nixdrv.StorePath, len(modKeys))
	for i, k := range modKeys {
		result[k] = paths[i]
	}
	return result, nil
}

// setupGOMODCACHE creates a temporary GOMODCACHE by merging all FOD outputs
// using recursive symlink copies.
func setupGOMODCACHE(fodPaths map[string]nixdrv.StorePath) (string, error) {
	gomodcache, err := os.MkdirTemp("", "gomodcache-")
	if err != nil {
		return "", err
	}
	for _, fodPath := range fodPaths {
		cmd := exec.Command("cp", "-rs", fodPath.String()+"/.", gomodcache)
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(gomodcache)
			return "", fmt.Errorf("merging FOD %s: %w\n%s", fodPath, err, out)
		}
	}
	return gomodcache, nil
}

// createPackageDrv creates a CA derivation for a single package.
func createPackageDrv(
	cfg Config,
	nix *nixdrv.NixTool,
	graph map[string]*ResolvedPkg,
	pkg *ResolvedPkg,
	overrides map[string]PackageOverride,
) error {
	drvName := nixdrv.PkgDrvName(pkg.ImportPath)
	bashStorePath := storeDirOf(cfg.BashBin)
	go2nixStorePath := storeDirOf(cfg.Go2NixBin)

	script := compileScript(cfg.Go2NixBin)

	drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
	drv.AddArg("-c")
	drv.AddArg(script)
	drv.AddCAOutput("out", "sha256", "nar")

	// Set env vars
	drv.SetEnv("importPath", pkg.ImportPath)

	// PATH must include go and go2nix binaries (compile-package calls `go` by name)
	goStoreDir := storeDirOf(cfg.GoBin)
	drv.SetEnv("PATH", goStoreDir+"/bin:"+go2nixStorePath+"/bin")

	// Source location
	if pkg.IsLocal {
		drv.SetEnv("modSrc", cfg.Src)
		drv.SetEnv("relDir", pkg.Subdir)
		drv.AddInputSrc(cfg.Src)
	} else {
		drv.SetEnv("modSrc", pkg.FodPath.String())
		relDir := nixdrv.EscapeModPath(pkg.FetchPath) + "@" + pkg.Version
		if pkg.Subdir != "" {
			relDir += "/" + pkg.Subdir
		}
		drv.SetEnv("relDir", relDir)
		drv.AddInputSrc(pkg.FodPath.String())
	}

	// Build importcfg entries
	var importcfgEntries []string
	for _, imp := range pkg.Imports {
		dep, ok := graph[imp]
		if !ok {
			// Stdlib package
			importcfgEntries = append(importcfgEntries,
				fmt.Sprintf("packagefile %s=%s/%s.a", imp, cfg.StdlibPath, imp))
			continue
		}
		// Non-stdlib dep — use CA placeholder
		placeholder := nixdrv.CAOutput(dep.DrvPath, "out")
		importcfgEntries = append(importcfgEntries,
			fmt.Sprintf("packagefile %s=%s/pkg.a", imp, placeholder.Render()))
		drv.AddInputDrv(dep.DrvPath.String(), "out")
	}
	drv.SetEnv("importcfg_entries", strings.Join(importcfgEntries, "\n"))

	// Forward build tags if set
	if cfg.Tags != "" {
		drv.SetEnv("tags", cfg.Tags)
	}

	// Forward CGO_ENABLED if set
	if cfg.CGOEnabled != "" {
		drv.SetEnv("CGO_ENABLED", cfg.CGOEnabled)
	}

	// Forward gcflags if set
	if cfg.GCFlags != "" {
		drv.SetEnv("gcflags", cfg.GCFlags)
	}

	// CA placeholder for out
	// We don't know our own drv path yet, so use standard placeholder
	drv.SetEnv("out", nixdrv.StandardOutput("out").Render())

	// Input sources
	drv.AddInputSrc(go2nixStorePath)
	drv.AddInputSrc(storeDirOf(cfg.GoBin))
	if cfg.StdlibPath != "" {
		drv.AddInputSrc(cfg.StdlibPath)
	}

	// Package overrides (cgo)
	if ov, ok := overrides[pkg.ImportPath]; ok {
		for _, nbi := range ov.NativeBuildInputs {
			drv.AddInputSrc(nbi)
		}
	}
	// Also check module-level overrides
	if pkg.ModKey != "" {
		modPath := strings.SplitN(pkg.ModKey, "@", 2)[0]
		if ov, ok := overrides[modPath]; ok {
			for _, nbi := range ov.NativeBuildInputs {
				drv.AddInputSrc(nbi)
			}
		}
	}

	drvPath, err := nix.DerivationAdd(drv)
	if err != nil {
		return err
	}
	pkg.DrvPath = drvPath
	return nil
}

// createFinalDrv creates the link (and optional collector) derivation.
// Returns the final .drv path to copy to $out.
func createFinalDrv(
	cfg Config,
	nix *nixdrv.NixTool,
	graph map[string]*ResolvedPkg,
	sorted []*ResolvedPkg,
) (nixdrv.StorePath, error) {
	// Find main packages
	var mainPkgs []*ResolvedPkg
	for _, pkg := range sorted {
		if pkg.Name == "main" {
			mainPkgs = append(mainPkgs, pkg)
		}
	}

	if len(mainPkgs) == 0 {
		return nixdrv.StorePath{}, fmt.Errorf("no main packages found")
	}

	// Create a link derivation for each main package
	var linkDrvPaths []nixdrv.StorePath
	var linkPlaceholders []string

	for _, mainPkg := range mainPkgs {
		drvPath, err := createLinkDrv(cfg, nix, graph, sorted, mainPkg, len(mainPkgs))
		if err != nil {
			return nixdrv.StorePath{}, fmt.Errorf("creating link for %s: %w", mainPkg.ImportPath, err)
		}
		linkDrvPaths = append(linkDrvPaths, drvPath)
		linkPlaceholders = append(linkPlaceholders, nixdrv.CAOutput(drvPath, "out").Render())
	}

	if len(linkDrvPaths) == 1 {
		return linkDrvPaths[0], nil
	}

	// Multiple binaries — create collector
	return createCollectorDrv(cfg, nix, linkDrvPaths, linkPlaceholders)
}

// createLinkDrv creates a link derivation for a main package.
func createLinkDrv(
	cfg Config,
	nix *nixdrv.NixTool,
	graph map[string]*ResolvedPkg,
	sorted []*ResolvedPkg,
	mainPkg *ResolvedPkg,
	numMains int,
) (nixdrv.StorePath, error) {
	// For single binary, use pname. For multiple binaries, derive name from import path.
	binName := cfg.PName
	if numMains > 1 {
		// Use last path component of import path (e.g., "mymod/cmd/server" → "server")
		parts := strings.Split(mainPkg.ImportPath, "/")
		binName = parts[len(parts)-1]
	}
	drvName := nixdrv.LinkDrvName(binName)
	bashStorePath := storeDirOf(cfg.BashBin)

	script := linkScript(cfg.GoBin, binName)

	drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
	drv.AddArg("-c")
	drv.AddArg(script)
	drv.AddCAOutput("out", "sha256", "nar")

	// Main package placeholder
	mainPlaceholder := nixdrv.CAOutput(mainPkg.DrvPath, "out")
	drv.SetEnv("mainPkg", mainPlaceholder.Render())
	drv.AddInputDrv(mainPkg.DrvPath.String(), "out")

	// Build importcfg with ALL transitive dependencies
	var importcfgEntries []string

	// Collect all transitive deps via the sorted list
	for _, pkg := range sorted {
		if pkg.Name == "main" && pkg != mainPkg {
			continue // skip other main packages
		}
		placeholder := nixdrv.CAOutput(pkg.DrvPath, "out")
		importcfgEntries = append(importcfgEntries,
			fmt.Sprintf("packagefile %s=%s/pkg.a", pkg.ImportPath, placeholder.Render()))
		drv.AddInputDrv(pkg.DrvPath.String(), "out")
	}

	// Add stdlib entries for all stdlib imports across all packages
	stdlibImports := collectStdlibImports(sorted, graph)
	for _, imp := range stdlibImports {
		importcfgEntries = append(importcfgEntries,
			fmt.Sprintf("packagefile %s=%s/%s.a", imp, cfg.StdlibPath, imp))
	}

	drv.SetEnv("importcfg_entries", strings.Join(importcfgEntries, "\n"))
	drv.SetEnv("ldflags", cfg.LDFlags)
	drv.SetEnv("out", nixdrv.StandardOutput("out").Render())

	// Input sources
	drv.AddInputSrc(storeDirOf(cfg.GoBin))
	if cfg.StdlibPath != "" {
		drv.AddInputSrc(cfg.StdlibPath)
	}

	return nix.DerivationAdd(drv)
}

// createCollectorDrv creates a collector derivation merging multiple link outputs.
func createCollectorDrv(
	cfg Config,
	nix *nixdrv.NixTool,
	linkDrvPaths []nixdrv.StorePath,
	linkPlaceholders []string,
) (nixdrv.StorePath, error) {
	drvName := nixdrv.CollectDrvName(cfg.PName)
	bashStorePath := storeDirOf(cfg.BashBin)

	script := collectScript(linkPlaceholders)

	drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
	drv.AddArg("-c")
	drv.AddArg(script)
	drv.AddCAOutput("out", "sha256", "nar")

	drv.SetEnv("out", nixdrv.StandardOutput("out").Render())

	for _, drvPath := range linkDrvPaths {
		drv.AddInputDrv(drvPath.String(), "out")
	}

	return nix.DerivationAdd(drv)
}

// collectStdlibImports returns all stdlib import paths used across all packages.
func collectStdlibImports(sorted []*ResolvedPkg, graph map[string]*ResolvedPkg) []string {
	seen := make(map[string]bool)
	for _, pkg := range sorted {
		for _, imp := range pkg.Imports {
			if _, inGraph := graph[imp]; !inGraph {
				// Not in our graph = stdlib
				seen[imp] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for imp := range seen {
		result = append(result, imp)
	}
	sort.Strings(result)
	return result
}

// storeDirOf returns the store path directory for a binary.
// E.g., "/nix/store/xxx-go/bin/go" → "/nix/store/xxx-go"
func storeDirOf(binPath string) string {
	// Find the 4th "/" (after /nix/store/hash-name)
	count := 0
	for i, c := range binPath {
		if c == '/' {
			count++
			if count == 4 {
				return binPath[:i]
			}
		}
	}
	return binPath
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o444)
}
