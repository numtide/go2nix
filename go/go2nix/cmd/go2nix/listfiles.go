package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/gofiles"
)

func runListFilesCmd(args []string) {
	fs := flag.NewFlagSet("list-files", flag.ExitOnError)
	tagsFlag := fs.String("tags", "", "comma-separated build tags")
	goVersionFlag := fs.String("go-version", "", "target Go toolchain version (e.g. \"1.25\"); defaults to `go env GOVERSION`")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		slog.Error("usage: go2nix list-files [-tags=...] [-go-version=...] <package-dir>")
		os.Exit(1)
	}

	result, err := gofiles.ListFiles(fs.Arg(0), *tagsFlag, resolveGoVersion(*goVersionFlag))
	if err != nil {
		slog.Error("list-files failed", "err", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		slog.Error("encoding JSON", "err", err)
		os.Exit(1)
	}
}
