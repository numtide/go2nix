package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/compile"
)

func runCompileModuleCmd(args []string) {
	fs := flag.NewFlagSet("compile-module", flag.ExitOnError)
	importCfg := fs.String("importcfg", "", "path to importcfg file (will be appended to)")
	outDir := fs.String("outdir", "", "output directory for .a files")
	tags := fs.String("tags", "", "comma-separated build tags")
	gcflags := fs.String("gcflags", "", "extra flags for go tool compile (space-separated)")
	trimPath := fs.String("trimpath", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	fs.Parse(args)

	if fs.NArg() != 1 || *importCfg == "" || *outDir == "" {
		slog.Error("usage: go2nix compile-module --importcfg FILE --outdir DIR [--tags TAGS] [--gcflags FLAGS] [--trimpath PATH] <module-root>")
		os.Exit(1)
	}

	opts := compile.CompileLocalOptions{
		ModuleRoot: fs.Arg(0),
		ImportCfg:  *importCfg,
		OutDir:     *outDir,
		Tags:       *tags,
		GCFlags:    *gcflags,
		TrimPath:   *trimPath,
	}

	if err := compile.CompileLocalPackages(opts); err != nil {
		slog.Error("compile-module failed", "err", err)
		os.Exit(1)
	}
}
