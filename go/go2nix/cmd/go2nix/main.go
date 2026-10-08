package main

import (
	"fmt"
	"log/slog"
	"os"
)

// useLocalToolchain pins the `go` child processes of a build subcommand to the
// toolchain on PATH. $GOROOT/go.env defaults GOTOOLCHAIN to auto, and under
// auto cmd/go's toolchain.Select, which runs before every subcommand (`go tool
// compile` and `go env` included), reads the go.mod or go.work above the
// working directory and downloads the toolchain it names when that is newer.
// The build subcommands start `go` from inside module sources with no network,
// so such a `toolchain` line, which cmd/go ignores in a dependency, would fail
// the build. A GOTOOLCHAIN already in the environment is kept; an empty one
// counts as unset, as in cmd/go's cfg.Getenv.
func useLocalToolchain() {
	if os.Getenv("GOTOOLCHAIN") == "" {
		_ = os.Setenv("GOTOOLCHAIN", "local")
	}
}

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
		useLocalToolchain()
		runCompilePackageCmd(os.Args[2:])
	case "check":
		runCheckLockfileCmd(os.Args[2:])
	case "resolve":
		useLocalToolchain()
		runResolveCmd(os.Args[2:])
	case "build-modinfo":
		runModinfoCmd(os.Args[2:])
	case "generate-test-main":
		runGenTestMainCmd(os.Args[2:])
	case "test-packages":
		useLocalToolchain()
		runTestPackagesCmd(os.Args[2:])
	case "link-binary":
		useLocalToolchain()
		runLinkBinaryCmd(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		fmt.Fprintf(os.Stderr, "usage: go2nix <generate|list-files|list-packages|compile-package|check|resolve|build-modinfo|generate-test-main|test-packages|link-binary> [flags]\n")
		os.Exit(1)
	}
}
