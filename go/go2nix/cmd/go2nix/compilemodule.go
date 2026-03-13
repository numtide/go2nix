package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/compile"
)

func runCompileModuleCmd(args []string) {
	fs := flag.NewFlagSet("compile-packages", flag.ExitOnError)
	importCfg := fs.String("import-cfg", "", "path to importcfg file (will be appended to)")
	outDir := fs.String("out-dir", "", "output directory for .a files")
	tags := fs.String("tags", "", "comma-separated build tags")
	gcflags := fs.String("gc-flags", "", "extra flags for go tool compile (space-separated)")
	trimPath := fs.String("trim-path", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	fs.Parse(args)

	if fs.NArg() != 1 || *importCfg == "" || *outDir == "" {
		slog.Error("usage: go2nix compile-packages --import-cfg FILE --out-dir DIR [--tags TAGS] [--gc-flags FLAGS] [--trim-path PATH] <module-root>")
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
		slog.Error("compile-packages failed", "err", err)
		os.Exit(1)
	}
}
