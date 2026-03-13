package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/mvscheck"
)

func runCheckLockfileCmd(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	lockfilePath := fs.String("lockfile", "", "path to go2nix.toml lockfile (lockfile consistency check)")
	fs.Parse(args)

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	var err error
	if *lockfilePath != "" {
		err = mvscheck.CheckLockfile(dir, *lockfilePath)
	} else {
		err = mvscheck.Check(dir)
	}
	if err != nil {
		slog.Error("check failed", "err", err)
		os.Exit(1)
	}
}
