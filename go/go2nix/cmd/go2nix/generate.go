package main

import (
	"flag"
	"log/slog"
	"os"
	"runtime"

	"github.com/numtide/go2nix/pkg/lockfilegen"
)

func runGenerateCmd(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	output := fs.String("o", "go2nix.toml", "output lockfile path")
	jobs := fs.Int("j", runtime.NumCPU(), "max parallel hash invocations")
	minimal := fs.Bool("minimal", false, "generate minimal lockfile (modules only, no packages)")
	fs.Parse(args)

	dirs := fs.Args()
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	if err := lockfilegen.Generate(dirs, *output, *jobs, *minimal); err != nil {
		slog.Error("generate failed", "err", err)
		os.Exit(1)
	}
}
