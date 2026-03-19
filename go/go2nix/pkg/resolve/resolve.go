// Package resolve implements the dynamic derivation resolve flow.
// It runs inside a recursive-nix wrapper derivation at build time,
// creating Nix derivations for each Go package via `nix derivation add`.
package resolve

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/sync/errgroup"

	"github.com/numtide/go2nix/internal/gonix/storepath"
	"github.com/numtide/go2nix/pkg/buildinfo"
	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/golist"
	"github.com/numtide/go2nix/pkg/lockfile"
	"github.com/numtide/go2nix/pkg/nixdrv"
)

// Config holds all configuration for the resolve flow.
type Config struct {
	Src          string // store path to source
	ModRoot      string // subdirectory within Src containing go.mod (default ".")
	LockFile     string // path to go2nix.toml lockfile
	System       string // e.g., "x86_64-linux"
	GoBin        string // path to go binary
	StdlibPath   string // path to pre-compiled stdlib
	NixBin       string // path to nix binary
	Go2NixBin    string // path to go2nix binary
	BashBin      string // path to bash binary
	CoreutilsBin string // path to coreutils binary (e.g., /nix/store/xxx-coreutils/bin/mkdir)
	PName        string // output binary name
	SubPackages  string // comma-separated sub-packages (default "./...")
	Tags         string // comma-separated build tags
	LDFlags      string // linker flags
	CGOEnabled   string // "0" or "1" to override cgo detection, empty for auto
	GCFlags      string // extra flags for go tool compile
	PGOProfile   string // store path to pprof CPU profile for PGO; empty disables PGO
	Overrides    string // JSON-encoded packageOverrides
	CACert       string // path to CA certificate bundle
	NetrcFile    string // path to .netrc file for private module authentication
	Output       string // $out path

	// coreutilsDir is the store path of coreutils, derived from CoreutilsBin.
	coreutilsDir string
	// ccDir is the store path of the CC wrapper, resolved at Resolve() time.
	ccDir string
	// ccPath is the full path to the C compiler (e.g., /nix/store/xxx/bin/cc).
	ccPath string
	// cxxPath is the full path to the C++ compiler (e.g., /nix/store/xxx/bin/c++).
	cxxPath string
	// allOverridePaths collects all nativeBuildInputs store paths for the link derivation.
	allOverridePaths []string
	// buildMode is "pie" or "exe", determined at Resolve() time from GOOS/GOARCH.
	buildMode string
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

	// Derive coreutils store path from the explicit binary path.
	cfg.coreutilsDir = storeDirOf(cfg.CoreutilsBin)

	// Find CC/CXX for cgo packages (stdenv provides these in the cc-wrapper).
	// Go's setextld uses CXX instead of CC when C++ files are present.
	if ccPath, err := exec.LookPath("cc"); err == nil {
		cfg.ccPath = ccPath
		cfg.ccDir = storeDirOf(filepath.Dir(ccPath))
	}
	if cxxPath, err := exec.LookPath("c++"); err == nil {
		cfg.cxxPath = cxxPath
	}

	// Determine default build mode (pie vs exe) from the Go toolchain,
	// matching cmd/go's platform.DefaultPIE logic.
	cfg.buildMode = resolveDefaultBuildMode(cfg.GoBin)
	slog.Info("build mode", "mode", cfg.buildMode)

	// Parse overrides
	overrides := map[string]PackageOverride{}
	if cfg.Overrides != "" && cfg.Overrides != "{}" {
		if err := json.Unmarshal([]byte(cfg.Overrides), &overrides); err != nil {
			return fmt.Errorf("parsing overrides: %w", err)
		}
	}
	// Collect all override paths for the link derivation (cgo external linking).
	// Use discoverInputPaths to follow propagated-build-inputs transitively.
	var allOverrideInputs []string
	for _, ov := range overrides {
		allOverrideInputs = append(allOverrideInputs, ov.NativeBuildInputs...)
	}
	_, _, cfg.allOverridePaths = discoverInputPaths(allOverrideInputs)

	resolveStart := time.Now()

	// Step 1: Read lockfile
	slog.Info("reading lockfile", "path", cfg.LockFile)
	lock, err := lockfile.Read(cfg.LockFile)
	if err != nil {
		return fmt.Errorf("reading lockfile: %w", err)
	}
	slog.Info("lockfile loaded", "modules", len(lock.Mod))

	// Step 2: Build module FOD derivations (in-process .drv path computation)
	t := time.Now()
	slog.Info("creating module FODs")
	fodDrvPaths, fodDrvs, err := buildModuleFODs(cfg, lock)
	if err != nil {
		return err
	}
	// Register FODs with the store in parallel
	if err := registerDerivations(nix, fodDrvs); err != nil {
		return fmt.Errorf("registering FODs: %w", err)
	}
	slog.Info("module FODs created", "count", len(fodDrvPaths), "elapsed", time.Since(t))

	// Step 3: Materialize FODs (build them)
	t = time.Now()
	slog.Info("building module FODs", "count", len(fodDrvPaths))
	fodPaths, err := buildFODs(nix, fodDrvPaths)
	if err != nil {
		return err
	}
	slog.Info("module FODs built", "elapsed", time.Since(t))

	// Step 4: Set up GOMODCACHE
	t = time.Now()
	gomodcache, err := setupGOMODCACHE(fodPaths)
	if err != nil {
		return fmt.Errorf("setting up GOMODCACHE: %w", err)
	}
	defer os.RemoveAll(gomodcache)
	slog.Info("GOMODCACHE set up", "elapsed", time.Since(t))

	// Step 5: Discover packages
	t = time.Now()
	slog.Info("discovering packages")
	subPkgs := []string{"./..."}
	if cfg.SubPackages != "" {
		subPkgs = strings.Split(cfg.SubPackages, ",")
	}

	// moduleRoot is the filesystem path where go.mod lives.
	// cfg.Src is the raw store path (used for AddInputSrc); cfg.ModRoot
	// is the subdirectory within it. We join them for filesystem operations
	// but keep cfg.Src separate for store-path references.
	moduleRoot := filepath.Join(cfg.Src, cfg.ModRoot)

	golistEnv := []string{
		"GOMODCACHE=" + gomodcache,
		"GONOSUMCHECK=*",
		"GOPROXY=off",
		"GOFLAGS=-mod=readonly",
		// The merged GOMODCACHE is assembled from symlinks to Nix store FOD
		// outputs (see setupGOMODCACHE). Go's //go:embed rejects symlinks by
		// default. This GODEBUG allows leaf-file symlinks to be followed.
		// It does NOT cover directory symlinks or symlinks inside directory
		// walks (those are silently skipped). A proper fix would copy files
		// instead of symlinking them in setupGOMODCACHE.
		// See: https://github.com/golang/go/issues/59924
		//      https://github.com/golang/go/issues/44507
		"GODEBUG=embedfollowsymlinks=1",
	}
	if cfg.CGOEnabled != "" {
		golistEnv = append(golistEnv, "CGO_ENABLED="+cfg.CGOEnabled)
	}

	pkgs, err := golist.ListDeps(golist.ListDepsOptions{
		Dir:       moduleRoot,
		GoBin:     cfg.GoBin,
		Tags:      cfg.Tags,
		Patterns:  subPkgs,
		Env:       golistEnv,
		KeepLocal: true,
		Compiled:  true,
	})
	if err != nil {
		return fmt.Errorf("discovering packages: %w", err)
	}
	slog.Info("packages discovered", "count", len(pkgs), "elapsed", time.Since(t))

	// Step 6: Build and topo-sort package graph
	graph, err := buildPackageGraph(pkgs, fodPaths, moduleRoot)
	if err != nil {
		return err
	}
	sorted, err := topoSort(graph)
	if err != nil {
		return fmt.Errorf("topological sort: %w", err)
	}
	slog.Info("packages sorted", "count", len(sorted))

	// Step 7a: Build package derivation specs in topo order.
	// .drv paths are computed in-process (microseconds per derivation)
	// instead of shelling out to `nix derivation add` (~33ms each).
	t = time.Now()
	slog.Info("building package derivations")
	var pkgDrvs []*nixdrv.Derivation
	localCount := 0
	thirdPartyCount := 0
	for _, pkg := range sorted {
		if pkg.IsLocal {
			localCount++
		} else {
			thirdPartyCount++
		}
		drv, err := buildPackageDrv(cfg, nix, graph, pkg, overrides)
		if err != nil {
			return fmt.Errorf("building derivation for %s: %w", pkg.ImportPath, err)
		}
		pkgDrvs = append(pkgDrvs, drv)
	}
	slog.Info("package derivations built", "local", localCount, "thirdParty", thirdPartyCount, "elapsed", time.Since(t))

	// Step 7b: Register package derivations with the store in parallel.
	// Nix does not validate that input drvs exist during `nix derivation add`
	// (validation is purely structural), so all drvs can be registered concurrently.
	t = time.Now()
	if err := registerDerivationsValidated(nix, pkgDrvs); err != nil {
		return fmt.Errorf("registering package derivations: %w", err)
	}
	slog.Info("package derivations registered", "count", len(pkgDrvs), "elapsed", time.Since(t))

	// Step 8+9: Build link/collector derivation specs
	t = time.Now()
	slog.Info("creating link derivation")
	finalDrvPath, finalDrvs, err := buildFinalDrv(cfg, graph, sorted)
	if err != nil {
		return err
	}
	for _, drv := range finalDrvs {
		if _, err := nix.DerivationAdd(drv); err != nil {
			return fmt.Errorf("registering final derivation: %w", err)
		}
	}
	slog.Info("link derivation created", "elapsed", time.Since(t))

	// Step 10: Copy .drv file to $out
	slog.Info("writing output", "drv", finalDrvPath.Absolute(), "out", cfg.Output)
	slog.Info("resolve total elapsed", "elapsed", time.Since(resolveStart))
	if err := copyFile(finalDrvPath.Absolute(), cfg.Output); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// buildModuleFODs creates a FOD derivation for each module in the lockfile.
// Computes .drv paths in-process (no subprocess calls).
// Returns modKey → .drv StorePath plus the list of Derivations for parallel registration.
func buildModuleFODs(cfg Config, lock *lockfile.Lockfile) (map[string]*storepath.StorePath, []*nixdrv.Derivation, error) {
	result := make(map[string]*storepath.StorePath, len(lock.Mod))
	drvs := make([]*nixdrv.Derivation, 0, len(lock.Mod))
	bashStorePath := storeDirOf(cfg.BashBin)

	for modKey, hash := range lock.Mod {
		modPath, version, ok := strings.Cut(modKey, "@")
		if !ok {
			return nil, nil, fmt.Errorf("invalid module key: %s", modKey)
		}

		fetchPath := modPath
		if r, ok := lock.Replace[modKey]; ok {
			fetchPath = r
		}

		drvName := nixdrv.ModDrvName(modKey)
		script := fodScript(cfg.GoBin, fetchPath, version, cfg.CACert, cfg.NetrcFile)

		drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
		drv.AddArg("-c")
		drv.AddArg(script)
		drv.AddFODOutput("out", "nar", hash)

		// Set env.out to empty; Nix fills in the computed path for FODs.
		drv.SetEnv("out", "")

		// Input sources: builder (bash), go binary, cacert, netrc
		drv.AddInputSrc(bashStorePath)
		goStoreDir := storeDirOf(cfg.GoBin)
		drv.AddInputSrc(goStoreDir)
		if cfg.CACert != "" {
			drv.AddInputSrc(storeDirOf(cfg.CACert))
		}
		if cfg.NetrcFile != "" {
			drv.AddInputSrc(storeDirOf(cfg.NetrcFile))
		}

		drvPath, err := drv.DrvPath()
		if err != nil {
			return nil, nil, fmt.Errorf("computing drv path for FOD %s: %w", modKey, err)
		}
		result[modKey] = drvPath
		drvs = append(drvs, drv)
	}
	return result, drvs, nil
}

// buildFODs materializes all FODs in a single batched nix build call.
// Returns modKey → output StorePath.
func buildFODs(nix *nixdrv.NixTool, fodDrvPaths map[string]*storepath.StorePath) (map[string]*storepath.StorePath, error) {
	if len(fodDrvPaths) == 0 {
		return map[string]*storepath.StorePath{}, nil
	}

	// Build a deterministic ordered list of modKeys and installables
	modKeys := make([]string, 0, len(fodDrvPaths))
	for k := range fodDrvPaths {
		modKeys = append(modKeys, k)
	}
	sort.Strings(modKeys)

	installables := make([]string, len(modKeys))
	for i, k := range modKeys {
		installables[i] = fodDrvPaths[k].Absolute() + "^out"
	}

	// Single nix build call — Nix handles parallelism internally
	paths, err := nix.Build(installables...)
	if err != nil {
		return nil, fmt.Errorf("building FODs: %w", err)
	}
	if len(paths) != len(modKeys) {
		return nil, fmt.Errorf("expected %d FOD outputs, got %d", len(modKeys), len(paths))
	}

	result := make(map[string]*storepath.StorePath, len(modKeys))
	for i, k := range modKeys {
		result[k] = paths[i]
	}
	return result, nil
}

// registerDerivations registers all derivations with the Nix store in parallel
// via `nix derivation add`. The .drv paths have already been computed in-process;
// this step writes the .drv files into the store so they can be built.
func registerDerivations(nix *nixdrv.NixTool, drvs []*nixdrv.Derivation) error {
	g := new(errgroup.Group)
	for _, drv := range drvs {
		g.Go(func() error {
			_, err := nix.DerivationAdd(drv)
			return err
		})
	}
	return g.Wait()
}

// registerDerivationsValidated registers derivations in parallel with bounded
// concurrency, and validates that our in-process .drv paths match what Nix returns.
func registerDerivationsValidated(nix *nixdrv.NixTool, drvs []*nixdrv.Derivation) error {
	g := new(errgroup.Group)
	// Use 2x CPU count for concurrency — these are I/O-bound subprocess calls,
	// not CPU-bound, so we can exceed GOMAXPROCS. SQLite WAL handles contention.
	g.SetLimit(max(runtime.NumCPU()*2, 16))
	for _, drv := range drvs {
		g.Go(func() error {
			nixPath, err := nix.DerivationAdd(drv)
			if err != nil {
				return err
			}
			ourPath, err := drv.DrvPath()
			if err != nil {
				return fmt.Errorf("re-computing drv path: %w", err)
			}
			if nixPath.Absolute() != ourPath.Absolute() {
				return fmt.Errorf("CA drv path mismatch:\n  ours: %s\n  nix:  %s\n  our aterm: %s",
					ourPath.Absolute(), nixPath.Absolute(), drv.DebugATerm())
			}
			return nil
		})
	}
	return g.Wait()
}

// setupGOMODCACHE creates a temporary GOMODCACHE by merging all FOD outputs
// into a single directory tree using symlinks.
//
// Each FOD output is a GOMODCACHE subtree (GOMODCACHE=$out). Multiple FODs
// share directory prefixes (e.g., cache/download/golang.org/) but never share
// leaf files since each FOD downloads a unique module@version. We walk each
// FOD, create real directories for intermediate paths, and symlink leaf files.
func setupGOMODCACHE(fodPaths map[string]*storepath.StorePath) (string, error) {
	gomodcache, err := os.MkdirTemp("", "gomodcache-")
	if err != nil {
		return "", err
	}
	for _, fodPath := range fodPaths {
		src := fodPath.Absolute()
		if err := symlinkTree(src, gomodcache); err != nil {
			os.RemoveAll(gomodcache)
			return "", fmt.Errorf("merging FOD %s: %w", fodPath.Absolute(), err)
		}
	}
	return gomodcache, nil
}

// symlinkTree recursively walks src and mirrors its structure into dst.
// Directories are created as real directories; files are symlinked.
func symlinkTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Symlink leaf files. If it already exists (shouldn't happen since
		// each FOD has unique module@version files), skip it.
		if _, err := os.Lstat(target); err == nil {
			return nil
		}
		return os.Symlink(path, target)
	})
}

// buildPackageDrv builds a CA derivation for a single package and computes its
// .drv path in-process. The derivation is returned for later parallel registration.
// For local packages, nix.StoreAdd is called to add filtered source to the store.
func buildPackageDrv(
	cfg Config,
	nix *nixdrv.NixTool,
	graph map[string]*ResolvedPkg,
	pkg *ResolvedPkg,
	overrides map[string]PackageOverride,
) (*nixdrv.Derivation, error) {
	_, modVersion, _ := strings.Cut(pkg.ModKey, "@")
	drvName := nixdrv.PkgDrvName(pkg.ImportPath, modVersion)
	bashStorePath := storeDirOf(cfg.BashBin)
	go2nixStorePath := storeDirOf(cfg.Go2NixBin)

	script := compileScript(cfg.Go2NixBin)

	drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
	drv.AddArg("-c")
	drv.AddArg(script)
	drv.AddCAOutput("out", "sha256", "nar")

	// Set env vars
	drv.SetEnv("importPath", pkg.ImportPath)
	if pkg.Name == "main" {
		drv.SetEnv("pflag", "main")
	}

	// PATH must include go, go2nix, and coreutils (mkdir, etc.)
	goStoreDir := storeDirOf(cfg.GoBin)
	pathParts := []string{goStoreDir + "/bin", go2nixStorePath + "/bin", cfg.coreutilsDir + "/bin"}
	// Collect nativeBuildInputs from overrides, discover actual dirs and
	// follow propagated-build-inputs for transitive dependencies.
	var overrideStorePaths []string
	if ov, ok := overrides[pkg.ImportPath]; ok {
		overrideStorePaths = append(overrideStorePaths, ov.NativeBuildInputs...)
	}
	if pkg.ModPath != "" {
		if ov, ok := overrides[pkg.ModPath]; ok {
			overrideStorePaths = append(overrideStorePaths, ov.NativeBuildInputs...)
		}
	}
	binDirs, pkgConfigDirs, allInputPaths := discoverInputPaths(overrideStorePaths)
	pathParts = append(pathParts, binDirs...)
	var pkgConfigParts []string
	pkgConfigParts = append(pkgConfigParts, pkgConfigDirs...)

	// Add C compiler for cgo packages (needed even without overrides).
	if len(pkg.CgoFiles) > 0 && cfg.ccDir != "" {
		pathParts = append(pathParts, cfg.ccDir+"/bin")
		drv.AddInputSrc(cfg.ccDir)
	}

	drv.SetEnv("PATH", strings.Join(pathParts, ":"))
	if len(pkgConfigParts) > 0 {
		drv.SetEnv("PKG_CONFIG_PATH", strings.Join(pkgConfigParts, ":"))
	}

	// Source location — scope per-package at file level for granular CA
	// invalidation. Each local package derivation references a store path
	// containing only its compilation-relevant files (source + embed targets),
	// so changing one .go file only invalidates the packages that contain it.
	if pkg.IsLocal {
		pkgDir := filepath.Join(cfg.Src, cfg.ModRoot, pkg.Subdir)
		filteredDir, err := createFilteredPkgDir(pkgDir, pkg)
		if err != nil {
			return nil, fmt.Errorf("creating filtered source for %s: %w", pkg.ImportPath, err)
		}
		defer os.RemoveAll(filteredDir)

		name := "gosrc-" + nixdrv.SanitizeName(pkg.ImportPath)
		pkgStorePath, err := nix.StoreAdd(name, filteredDir)
		if err != nil {
			return nil, fmt.Errorf("adding package source for %s: %w", pkg.ImportPath, err)
		}
		drv.SetEnv("modSrc", pkgStorePath.Absolute())
		drv.SetEnv("relDir", ".")
		drv.AddInputSrc(pkgStorePath.Absolute())
	} else {
		drv.SetEnv("modSrc", pkg.FodPath.Absolute())
		escapedPath, err := module.EscapePath(pkg.FetchPath)
		if err != nil {
			return nil, fmt.Errorf("escaping module path %s: %w", pkg.FetchPath, err)
		}
		relDir := escapedPath + "@" + pkg.Version
		if pkg.Subdir != "" {
			relDir += "/" + pkg.Subdir
		}
		drv.SetEnv("relDir", relDir)
		drv.AddInputSrc(pkg.FodPath.Absolute())
	}

	// Build importcfg entries.
	// With -compiled, go list already includes cgo-generated implicit imports
	// (runtime/cgo, syscall, unsafe) in the Imports field.
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
		drv.AddInputDrv(dep.DrvPath.Absolute(), "out")
	}
	drv.SetEnv("importcfg_entries", strings.Join(importcfgEntries, "\n"))

	// Forward build tags if set
	if cfg.Tags != "" {
		drv.SetEnv("tags", cfg.Tags)
	}

	// Forward Go language version for -lang flag (from module's go.mod).
	if pkg.GoVersion != "" {
		drv.SetEnv("goVersion", compile.LangVersion(pkg.GoVersion))
	}

	// Forward CGO_ENABLED if set
	if cfg.CGOEnabled != "" {
		drv.SetEnv("CGO_ENABLED", cfg.CGOEnabled)
	}

	// PGO profile — passed to every package's compile step.
	if cfg.PGOProfile != "" {
		drv.SetEnv("pgoProfile", cfg.PGOProfile)
		drv.AddInputSrc(storeDirOf(cfg.PGOProfile))
	}

	// Forward gcflags. When building PIE, pass -shared to the compiler
	// so it generates position-independent code, matching cmd/go behavior.
	gcflags := cfg.GCFlags
	if cfg.buildMode == "pie" {
		if gcflags != "" {
			gcflags = "-shared " + gcflags
		} else {
			gcflags = "-shared"
		}
	}
	if gcflags != "" {
		drv.SetEnv("gcflags", gcflags)
	}

	// CA placeholder for out
	// We don't know our own drv path yet, so use standard placeholder
	drv.SetEnv("out", nixdrv.StandardOutput("out").Render())

	// Input sources
	drv.AddInputSrc(bashStorePath)
	drv.AddInputSrc(cfg.coreutilsDir)
	drv.AddInputSrc(go2nixStorePath)
	drv.AddInputSrc(storeDirOf(cfg.GoBin))
	drv.AddInputSrc(cfg.StdlibPath)

	// Package overrides (cgo) — add all discovered paths (including transitive).
	for _, p := range allInputPaths {
		drv.AddInputSrc(p)
	}

	drvPath, err := drv.DrvPath()
	if err != nil {
		return nil, fmt.Errorf("computing drv path for %s: %w", pkg.ImportPath, err)
	}
	pkg.DrvPath = drvPath
	return drv, nil
}

// buildFinalDrv creates the link (and optional collector) derivation.
// Returns the final .drv path and all derivations for parallel registration.
func buildFinalDrv(
	cfg Config,
	graph map[string]*ResolvedPkg,
	sorted []*ResolvedPkg,
) (*storepath.StorePath, []*nixdrv.Derivation, error) {
	// Find main packages
	var mainPkgs []*ResolvedPkg
	for _, pkg := range sorted {
		if pkg.Name == "main" {
			mainPkgs = append(mainPkgs, pkg)
		}
	}

	if len(mainPkgs) == 0 {
		return nil, nil, fmt.Errorf("no main packages found")
	}

	// Build a link derivation for each main package
	var linkDrvPaths []*storepath.StorePath
	var linkPlaceholders []string
	var drvs []*nixdrv.Derivation

	for _, mainPkg := range mainPkgs {
		drvPath, drv, err := buildLinkDrv(cfg, graph, sorted, mainPkg, len(mainPkgs))
		if err != nil {
			return nil, nil, fmt.Errorf("building link for %s: %w", mainPkg.ImportPath, err)
		}
		linkDrvPaths = append(linkDrvPaths, drvPath)
		linkPlaceholders = append(linkPlaceholders, nixdrv.CAOutput(drvPath, "out").Render())
		drvs = append(drvs, drv)
	}

	if len(linkDrvPaths) == 1 {
		return linkDrvPaths[0], drvs, nil
	}

	// Multiple binaries — create collector
	collectorPath, collectorDrv, err := buildCollectorDrv(cfg, linkDrvPaths, linkPlaceholders)
	if err != nil {
		return nil, nil, err
	}
	drvs = append(drvs, collectorDrv)
	return collectorPath, drvs, nil
}

// buildLinkDrv builds a link derivation for a main package.
// Returns the .drv path and the Derivation for parallel registration.
func buildLinkDrv(
	cfg Config,
	graph map[string]*ResolvedPkg,
	sorted []*ResolvedPkg,
	mainPkg *ResolvedPkg,
	numMains int,
) (*storepath.StorePath, *nixdrv.Derivation, error) {
	// For single binary, use pname. For multiple binaries, derive name from import path.
	binName := cfg.PName
	if numMains > 1 {
		// Use last path component of import path (e.g., "mymod/cmd/server" → "server")
		parts := strings.Split(mainPkg.ImportPath, "/")
		binName = parts[len(parts)-1]
	}
	drvName := nixdrv.LinkDrvName(binName)
	bashStorePath := storeDirOf(cfg.BashBin)

	script := linkScript(cfg.GoBin, binName, cfg.buildMode)

	drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
	drv.AddArg("-c")
	drv.AddArg(script)
	drv.AddCAOutput("out", "sha256", "nar")

	// Main package placeholder
	mainPlaceholder := nixdrv.CAOutput(mainPkg.DrvPath, "out")
	drv.SetEnv("mainPkg", mainPlaceholder.Render())
	drv.AddInputDrv(mainPkg.DrvPath.Absolute(), "out")

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
		drv.AddInputDrv(pkg.DrvPath.Absolute(), "out")
	}

	// Add all stdlib entries — the linker needs the full transitive closure.
	stdlibImports, err := collectStdlibImports(cfg.StdlibPath)
	if err != nil {
		return nil, nil, err
	}
	for _, imp := range stdlibImports {
		importcfgEntries = append(importcfgEntries,
			fmt.Sprintf("packagefile %s=%s/%s.a", imp, cfg.StdlibPath, imp))
	}

	// Add modinfo so go version -m shows module dependencies.
	modinfoLine, err := generateModinfo(cfg, sorted)
	if err != nil {
		slog.Warn("modinfo generation failed, skipping", "err", err)
	} else {
		importcfgEntries = append(importcfgEntries, modinfoLine)
	}

	drv.SetEnv("importcfg_entries", strings.Join(importcfgEntries, "\n"))
	drv.SetEnv("ldflags", cfg.LDFlags)

	// Propagate sanitizer flags (-race, -msan, -asan) from gcflags to the
	// linker, matching cmd/go behavior (init.go forcedLdflags).
	var sanitizerFlags []string
	for _, f := range strings.Fields(cfg.GCFlags) {
		switch f {
		case "-race", "-msan", "-asan":
			sanitizerFlags = append(sanitizerFlags, f)
		}
	}
	if len(sanitizerFlags) > 0 {
		drv.SetEnv("sanitizerLinkFlags", strings.Join(sanitizerFlags, " "))
	}

	// Set GOROOT so the linker embeds runtime.defaultGOROOT,
	// enabling runtime.GOROOT() in the resulting binary.
	goroot := queryGoEnv(cfg.GoBin, "GOROOT")
	if goroot != "" {
		drv.SetEnv("goroot", goroot)
	}

	// GODEBUG default from go.mod's go directive — the linker embeds
	// this via -X=runtime.godebugDefault=<value> (gc.go:624-626).
	// go list populates DefaultGODEBUG for main packages.
	if mainPkg.DefaultGODEBUG != "" {
		drv.SetEnv("godebugDefault", mainPkg.DefaultGODEBUG)
	}

	drv.SetEnv("out", nixdrv.StandardOutput("out").Render())

	// Check if any package in the graph uses cgo — the linker needs CC for
	// external linking regardless of whether packageOverrides were specified.
	// Also track C++ usage: Go's setextld uses CXX instead of CC when C++
	// files are present (needed to link C++ standard libraries).
	hasCgo := false
	hasCxx := false
	for _, pkg := range sorted {
		if len(pkg.CgoFiles) > 0 {
			hasCgo = true
		}
		if len(pkg.CXXFiles) > 0 {
			hasCxx = true
		}
	}

	// PATH: coreutils + CC for external linking (cgo)
	pathParts := []string{cfg.coreutilsDir + "/bin"}
	if hasCgo && cfg.ccPath != "" {
		// Pass -extld explicitly so the linker uses the correct compiler,
		// matching cmd/go's setextld behavior (see gc.go).
		extld := cfg.ccPath
		if hasCxx && cfg.cxxPath != "" {
			extld = cfg.cxxPath
		}
		drv.SetEnv("extld", extld)
		pathParts = append(pathParts, cfg.ccDir+"/bin")
		drv.AddInputSrc(cfg.ccDir)
	}
	drv.SetEnv("PATH", strings.Join(pathParts, ":"))

	// Input sources
	drv.AddInputSrc(bashStorePath)
	drv.AddInputSrc(cfg.coreutilsDir)
	drv.AddInputSrc(storeDirOf(cfg.GoBin))
	drv.AddInputSrc(cfg.StdlibPath)
	// Add override paths (libraries needed for external linking)
	for _, p := range cfg.allOverridePaths {
		drv.AddInputSrc(p)
	}

	drvPath, err := drv.DrvPath()
	if err != nil {
		return nil, nil, fmt.Errorf("computing link drv path: %w", err)
	}
	return drvPath, drv, nil
}

// buildCollectorDrv builds a collector derivation merging multiple link outputs.
// Returns the .drv path and Derivation for parallel registration.
func buildCollectorDrv(
	cfg Config,
	linkDrvPaths []*storepath.StorePath,
	linkPlaceholders []string,
) (*storepath.StorePath, *nixdrv.Derivation, error) {
	drvName := nixdrv.CollectDrvName(cfg.PName)
	bashStorePath := storeDirOf(cfg.BashBin)

	script := collectScript(linkPlaceholders)

	drv := nixdrv.NewDerivation(drvName, cfg.System, bashStorePath+"/bin/bash")
	drv.AddArg("-c")
	drv.AddArg(script)
	drv.AddCAOutput("out", "sha256", "nar")

	drv.SetEnv("out", nixdrv.StandardOutput("out").Render())
	drv.SetEnv("PATH", cfg.coreutilsDir+"/bin")

	drv.AddInputSrc(bashStorePath)
	drv.AddInputSrc(cfg.coreutilsDir)
	for _, drvPath := range linkDrvPaths {
		drv.AddInputDrv(drvPath.Absolute(), "out")
	}

	drvPath, err := drv.DrvPath()
	if err != nil {
		return nil, nil, fmt.Errorf("computing collector drv path: %w", err)
	}
	return drvPath, drv, nil
}

// collectStdlibImports returns all stdlib import paths from the pre-compiled stdlib.
// The linker needs the full transitive closure including internal/ and vendor/
// packages (e.g., net/http depends on internal/poll). Scanning all .a files is simplest;
// extra entries are harmless — the linker ignores packages it doesn't need.
func collectStdlibImports(stdlibPath string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(stdlibPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".a") {
			return nil
		}
		rel, err := filepath.Rel(stdlibPath, path)
		if err != nil {
			return err
		}
		importPath := strings.TrimSuffix(rel, ".a")
		result = append(result, importPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning stdlib at %s: %w", stdlibPath, err)
	}
	sort.Strings(result)
	return result, nil
}

// storeDirOf returns the top-level store path for a path inside the Nix store.
// E.g., "/nix/store/xxx-go/bin/go" → "/nix/store/xxx-go"
func storeDirOf(path string) string {
	prefix := storepath.StoreDir + "/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	// Find end of the store entry name (hash-name component).
	rest := path[len(prefix):]
	if idx := strings.Index(rest, "/"); idx >= 0 {
		return path[:len(prefix)+idx]
	}
	return path
}

// discoverInputPaths inspects store paths to determine which environment
// directories they provide, and follows nix-support/propagated-build-inputs
// for transitive dependencies.
func discoverInputPaths(storePaths []string) (binDirs, pkgConfigDirs, allPaths []string) {
	visited := make(map[string]bool)
	var walk func(string)
	walk = func(p string) {
		if visited[p] {
			return
		}
		visited[p] = true
		allPaths = append(allPaths, p)

		if isDir(p + "/bin") {
			binDirs = append(binDirs, p+"/bin")
		}
		if isDir(p + "/lib/pkgconfig") {
			pkgConfigDirs = append(pkgConfigDirs, p+"/lib/pkgconfig")
		}

		// Follow propagated-build-inputs (space-separated store paths).
		data, err := os.ReadFile(p + "/nix-support/propagated-build-inputs")
		if err != nil {
			return
		}
		for _, dep := range strings.Fields(string(data)) {
			walk(dep)
		}
	}
	for _, sp := range storePaths {
		walk(sp)
	}
	return
}

// isDir returns true if path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// resolveDefaultBuildMode queries the Go toolchain for GOOS/GOARCH and
// returns the default build mode ("pie" or "exe"), matching cmd/go's
// platform.DefaultPIE logic.
func resolveDefaultBuildMode(goBin string) string {
	goos := queryGoEnv(goBin, "GOOS")
	goarch := queryGoEnv(goBin, "GOARCH")
	return compile.DefaultBuildMode(goos, goarch)
}

// queryGoEnv runs `go env <key>` and returns the result.
func queryGoEnv(goBin, key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	out, err := exec.Command(goBin, "env", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// generateModinfo constructs the modinfo importcfg line from the package graph.
func generateModinfo(cfg Config, sorted []*ResolvedPkg) (string, error) {
	// Get Go toolchain version.
	goVersion := queryGoEnv(cfg.GoBin, "GOVERSION")

	// Collect unique third-party modules from the graph.
	seen := make(map[string]bool)
	var deps []buildinfo.ModDep
	for _, pkg := range sorted {
		if pkg.IsLocal || pkg.ModKey == "" {
			continue
		}
		if seen[pkg.ModKey] {
			continue
		}
		seen[pkg.ModKey] = true
		dep := buildinfo.ModDep{
			Path:    pkg.ModPath,
			Version: pkg.Version,
		}
		// If the module is replaced (FetchPath differs from ModPath),
		// record the replacement.
		if pkg.FetchPath != pkg.ModPath {
			dep.Replace = &buildinfo.ModDep{
				Path:    pkg.FetchPath,
				Version: pkg.Version,
			}
		}
		deps = append(deps, dep)
	}

	return buildinfo.GenerateModinfo(filepath.Join(cfg.Src, cfg.ModRoot), goVersion, deps)
}

// createFilteredPkgDir creates a temporary directory containing only the files
// needed to compile a package: source files (GoFiles, CgoFiles, etc.) and embed
// targets. This enables file-level granularity for CA invalidation — changes to
// unrelated files in the same directory tree won't affect this package's store hash.
func createFilteredPkgDir(pkgDir string, pkg *ResolvedPkg) (string, error) {
	tmpDir, err := os.MkdirTemp("", "gosrc-*")
	if err != nil {
		return "", err
	}

	// Collect all basenames (source files in the immediate package directory).
	var files []string
	files = append(files, pkg.GoFiles...)
	files = append(files, pkg.CgoFiles...)
	files = append(files, pkg.CFiles...)
	files = append(files, pkg.CXXFiles...)
	files = append(files, pkg.FFiles...)
	files = append(files, pkg.SFiles...)
	files = append(files, pkg.HFiles...)
	files = append(files, pkg.SysoFiles...)

	// Copy source files (basenames).
	for _, f := range files {
		if err := linkOrCopyFile(filepath.Join(pkgDir, f), filepath.Join(tmpDir, f)); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("copying %s: %w", f, err)
		}
	}

	// Copy embed target files (may include subdirectory paths like "templates/index.html").
	for _, f := range pkg.EmbedFiles {
		dst := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("creating dir for embed file %s: %w", f, err)
		}
		if err := linkOrCopyFile(filepath.Join(pkgDir, f), dst); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("copying embed file %s: %w", f, err)
		}
	}

	return tmpDir, nil
}

// linkOrCopyFile creates dst as a hard link to src. Falls back to a regular
// copy if hard linking fails (e.g., cross-device). Hard links avoid memory
// overhead for large embed assets and are resolved to real file content by
// nix store add.
func linkOrCopyFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o444)
}
