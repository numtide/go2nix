package main

import (
	"fmt"
	"log/slog"
	"os"
)

func main() {
	// Default slog handler: text, info level.
	// Use GO2NIX_DEBUG=1 for debug output.
	level := slog.LevelInfo
	if os.Getenv("GO2NIX_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if len(os.Args) < 2 {
		runGenerateCmd(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "generate":
		runGenerateCmd(os.Args[2:])
	case "list-files":
		runListFilesCmd(os.Args[2:])
	case "list-packages":
		runListPackagesCmd(os.Args[2:])
	case "compile-package":
		runCompilePackageCmd(os.Args[2:])
	case "compile-packages":
		runCompileModuleCmd(os.Args[2:])
	case "check":
		runCheckLockfileCmd(os.Args[2:])
	case "resolve":
		runResolveCmd(os.Args[2:])
	case "build-modinfo":
		runModinfoCmd(os.Args[2:])
	case "generate-test-main":
		runGenTestMainCmd(os.Args[2:])
	case "test-packages":
		runTestPackagesCmd(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		fmt.Fprintf(os.Stderr, "usage: go2nix <generate|list-files|list-packages|compile-package|compile-packages|check|resolve|build-modinfo|generate-test-main|test-packages> [flags]\n")
		os.Exit(1)
	}
}
