package compile

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/numtide/go2nix/pkg/localpkgs"
)

// CompileLocalOptions configures parallel compilation of local packages.
type CompileLocalOptions struct {
	ModuleRoot string
	ImportCfg  string // path to importcfg file (will be appended to)
	OutDir     string // output directory for .a files
	Tags       string
	GCFlags    string
	TrimPath   string
}

// CompileLocalPackages discovers and compiles all local library packages
// in parallel, respecting dependency ordering via a DAG-aware scheduler.
func CompileLocalPackages(opts CompileLocalOptions) error {
	pkgs, err := localpkgs.ListLocalPackages(opts.ModuleRoot, opts.Tags)
	if err != nil {
		return fmt.Errorf("listing local packages: %w", err)
	}

	// Filter to library packages only.
	var libs []*localpkgs.LocalPkg
	pkgMap := map[string]*localpkgs.LocalPkg{}
	for _, p := range pkgs {
		if !p.IsCommand {
			libs = append(libs, p)
			pkgMap[p.ImportPath] = p
		}
	}

	if len(libs) == 0 {
		return nil
	}

	// Create output directories.
	for _, p := range libs {
		outPath := filepath.Join(opts.OutDir, p.ImportPath+".a")
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
	}

	// Pre-populate importcfg with all local package entries.
	// go tool compile reads importcfg at startup but only opens .a files
	// when resolving an import. Since we respect DAG ordering, a dep's
	// .a file will exist by the time any dependent package needs it.
	f, err := os.OpenFile(opts.ImportCfg, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening importcfg: %w", err)
	}
	for _, p := range libs {
		outPath := filepath.Join(opts.OutDir, p.ImportPath+".a")
		fmt.Fprintf(f, "packagefile %s=%s\n", p.ImportPath, outPath)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing importcfg: %w", err)
	}

	// Build reverse-dependency map for scheduling.
	revDeps := map[string][]string{}
	inDeg := map[string]int{}
	for _, p := range libs {
		count := 0
		for _, dep := range p.LocalDeps {
			if _, isLib := pkgMap[dep]; isLib {
				count++
				revDeps[dep] = append(revDeps[dep], p.ImportPath)
			}
		}
		inDeg[p.ImportPath] = count
	}

	// Determine worker count: respect NIX_BUILD_CORES, fall back to NumCPU.
	workers := maxWorkers(len(libs))

	// DAG-aware parallel compilation.
	// The main goroutine acts as scheduler: it collects results and
	// dispatches newly-ready packages to the worker pool.
	type result struct {
		importPath string
		err        error
	}

	results := make(chan result)
	sem := make(chan struct{}, workers)
	inFlight := 0

	compileOne := func(pkg *localpkgs.LocalPkg) {
		inFlight++
		go func() {
			sem <- struct{}{} // acquire worker slot
			outPath := filepath.Join(opts.OutDir, pkg.ImportPath+".a")
			slog.Info("compiling local library", "pkg", pkg.ImportPath)
			err := CompilePackage(Options{
				ImportPath: pkg.ImportPath,
				SrcDir:     pkg.SrcDir,
				Output:     outPath,
				ImportCfg:  opts.ImportCfg,
				TrimPath:   opts.TrimPath,
				Tags:       opts.Tags,
				GCFlags:    opts.GCFlags,
			})
			<-sem // release worker slot
			results <- result{pkg.ImportPath, err}
		}()
	}

	// Seed: launch all packages with no local library deps.
	for _, p := range libs {
		if inDeg[p.ImportPath] == 0 {
			compileOne(p)
		}
	}

	// Process results and schedule dependents.
	compiled := 0
	for inFlight > 0 {
		r := <-results
		inFlight--
		if r.err != nil {
			// Drain in-flight goroutines to avoid leak.
			for inFlight > 0 {
				<-results
				inFlight--
			}
			return fmt.Errorf("compiling %s: %w", r.importPath, r.err)
		}
		compiled++
		slog.Debug("compiled", "pkg", r.importPath, "progress", fmt.Sprintf("%d/%d", compiled, len(libs)))

		// Schedule dependents whose deps are now all compiled.
		for _, depIP := range revDeps[r.importPath] {
			inDeg[depIP]--
			if inDeg[depIP] == 0 {
				compileOne(pkgMap[depIP])
			}
		}
	}

	return nil
}

// maxWorkers returns the parallelism limit, respecting NIX_BUILD_CORES.
func maxWorkers(n int) int {
	w := runtime.NumCPU()
	if s := os.Getenv("NIX_BUILD_CORES"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			w = v
		}
	}
	if w > n {
		w = n
	}
	if w < 1 {
		w = 1
	}
	return w
}
