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
	mode := fs.String("mode", "dag", "builder mode: dag (default), dynamic")
	fs.Parse(args)

	switch *mode {
	case "dag", "dynamic":
	default:
		slog.Error("invalid mode", "mode", *mode, "valid", "dag, dynamic")
		os.Exit(1)
	}

	dirs := fs.Args()
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	if err := lockfilegen.Generate(dirs, *output, *jobs, *mode); err != nil {
		slog.Error("generate failed", "err", err)
		os.Exit(1)
	}
}
