package compile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/numtide/go2nix/pkg/localpkgs"
	"golang.org/x/sync/errgroup"
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
	for _, p := range pkgs {
		if !p.IsCommand {
			libs = append(libs, p)
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
	f, err := os.OpenFile(opts.ImportCfg, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening importcfg: %w", err)
	}
	for _, p := range libs {
		outPath := filepath.Join(opts.OutDir, p.ImportPath+".a")
		if _, err := fmt.Fprintf(f, "packagefile %s=%s\n", p.ImportPath, outPath); err != nil {
			f.Close()
			return fmt.Errorf("writing importcfg: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing importcfg: %w", err)
	}

	// DAG-aware parallel compilation using errgroup.
	// Each goroutine waits for its local library deps to finish (via done
	// channels) before compiling, so DAG order is naturally respected.
	done := make(map[string]chan struct{}, len(libs))
	for _, p := range libs {
		done[p.ImportPath] = make(chan struct{})
	}

	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(maxWorkers(len(libs)))

	for _, p := range libs {
		g.Go(func() error {
			// Wait for local library deps to finish.
			for _, dep := range p.LocalDeps {
				if ch, ok := done[dep]; ok {
					select {
					case <-ch:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}

			outPath := filepath.Join(opts.OutDir, p.ImportPath+".a")
			slog.Info("compiling local library", "pkg", p.ImportPath)
			err := CompileGoPackage(Options{
				ImportPath: p.ImportPath,
				SrcDir:     p.SrcDir,
				Output:     outPath,
				ImportCfg:  opts.ImportCfg,
				TrimPath:   opts.TrimPath,
				Tags:       opts.Tags,
				GCFlags:    opts.GCFlags,
			})
			if err != nil {
				return fmt.Errorf("compiling %s: %w", p.ImportPath, err)
			}

			close(done[p.ImportPath]) // signal dependents
			return nil
		})
	}

	return g.Wait()
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
