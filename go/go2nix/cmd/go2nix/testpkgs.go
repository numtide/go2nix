package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/testrunner"
)

func runTestPackagesCmd(args []string) {
	fs := flag.NewFlagSet("test-packages", flag.ExitOnError)
	importCfg := fs.String("import-cfg", "", "path to importcfg file")
	localDir := fs.String("local-dir", "", "directory with compiled local .a files")
	tags := fs.String("tags", "", "comma-separated build tags")
	gcflags := fs.String("gc-flags", "", "extra flags for go tool compile")
	trimPath := fs.String("trim-path", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	checkFlags := fs.String("check-flags", "", "flags to pass to test binaries")
	fs.Parse(args)

	if fs.NArg() != 1 || *importCfg == "" || *localDir == "" {
		slog.Error("usage: go2nix test-packages --import-cfg FILE --local-dir DIR [--tags TAGS] [--gc-flags FLAGS] [--trim-path PATH] [--check-flags FLAGS] <module-root>")
		os.Exit(1)
	}

	opts := testrunner.Options{
		ModuleRoot: fs.Arg(0),
		ImportCfg:  *importCfg,
		LocalDir:   *localDir,
		TrimPath:   *trimPath,
		Tags:       *tags,
		GCFlags:    *gcflags,
		CheckFlags: *checkFlags,
	}

	if err := testrunner.Run(opts); err != nil {
		slog.Error("test-packages failed", "err", err)
		os.Exit(1)
	}
}
