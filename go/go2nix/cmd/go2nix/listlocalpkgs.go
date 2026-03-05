package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/localpkgs"
)

func runlistLocalPackagesCmd(args []string) {
	fs := flag.NewFlagSet("list-local-packages", flag.ExitOnError)
	tagsFlag := fs.String("tags", "", "comma-separated build tags")
	fs.Parse(args)
	if fs.NArg() != 1 {
		slog.Error("usage: gob list-local-packages [-tags=...] <module-root>")
		os.Exit(1)
	}

	pkgs, err := localpkgs.ListLocalPackages(fs.Arg(0), *tagsFlag)
	if err != nil {
		slog.Error("list-local-packages failed", "err", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pkgs); err != nil {
		slog.Error("encoding JSON", "err", err)
		os.Exit(1)
	}
}
