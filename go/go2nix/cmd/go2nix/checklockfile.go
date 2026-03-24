package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/mvscheck"
)

func runCheckLockfileCmd(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	lockfilePath := fs.String("lockfile", "go2nix.toml", "path to go2nix.toml lockfile")
	_ = fs.Parse(args)

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	if err := mvscheck.CheckLockfile(dir, *lockfilePath); err != nil {
		slog.Error("check failed", "err", err)
		os.Exit(1)
	}
}
