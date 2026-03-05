package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/compile"
)

func runCompilePackageCmd(args []string) {
	fs := flag.NewFlagSet("compile-package", flag.ExitOnError)
	importPath := fs.String("import-path", "", "Go import path for the package")
	srcDir := fs.String("src-dir", "", "directory containing source files")
	output := fs.String("output", "", "output .a archive path")
	importCfg := fs.String("importcfg", "", "path to importcfg file")
	tags := fs.String("tags", "", "comma-separated build tags")
	gcflags := fs.String("gcflags", "", "extra flags for go tool compile (space-separated)")
	trimPath := fs.String("trimpath", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	pFlag := fs.String("p", "", "override -p flag (default: import-path)")
	fs.Parse(args)

	if *importPath == "" || *srcDir == "" || *output == "" || *importCfg == "" {
		slog.Error("usage: gob compile-package --import-path PATH --src-dir DIR --output FILE --importcfg FILE [--tags TAGS] [--trimpath PATH] [--p FLAG]")
		os.Exit(1)
	}

	opts := compile.Options{
		ImportPath: *importPath,
		PFlag:      *pFlag,
		SrcDir:     *srcDir,
		Output:     *output,
		ImportCfg:  *importCfg,
		TrimPath:   *trimPath,
		Tags:       *tags,
		GCFlags:    *gcflags,
	}

	if err := compile.CompilePackage(opts); err != nil {
		slog.Error("compile-package failed", "err", err, "pkg", *importPath)
		os.Exit(1)
	}
}
