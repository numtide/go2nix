package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/numtide/go2nix/pkg/buildinfo"
	"github.com/numtide/go2nix/pkg/lockfile"
)

func runModinfoCmd(args []string) {
	fs := flag.NewFlagSet("build-modinfo", flag.ExitOnError)
	lockfilePath := fs.String("lockfile", "", "path to go2nix.toml lockfile")
	goBin := fs.String("go", "", "path to go binary (default: from PATH)")
	_ = fs.Parse(args)

	moduleRoot := fs.Arg(0)
	if moduleRoot == "" || *lockfilePath == "" {
		slog.Error("usage: go2nix build-modinfo --lockfile FILE [--go GO] MODULE_ROOT")
		os.Exit(1)
	}

	// Get Go toolchain version.
	gobin := *goBin
	if gobin == "" {
		gobin = "go"
	}
	out, err := exec.Command(gobin, "env", "GOVERSION").Output()
	if err != nil {
		slog.Error("go env GOVERSION failed", "err", err)
		os.Exit(1)
	}
	goVersion := strings.TrimSpace(string(out))

	// Read lockfile for dependency modules.
	lock, err := lockfile.Read(*lockfilePath)
	if err != nil {
		slog.Error("reading lockfile", "err", err)
		os.Exit(1)
	}

	var deps []buildinfo.ModDep
	for modKey := range lock.Mod {
		modPath, version, ok := strings.Cut(modKey, "@")
		if !ok {
			continue
		}
		dep := buildinfo.ModDep{
			Path:    modPath,
			Version: version,
		}
		if replacePath, ok := lock.Replace[modKey]; ok {
			dep.Replace = &buildinfo.ModDep{
				Path:    replacePath,
				Version: version,
			}
		}
		deps = append(deps, dep)
	}

	line, err := buildinfo.GenerateModinfo(moduleRoot, goVersion, deps)
	if err != nil {
		slog.Error("generating modinfo", "err", err)
		os.Exit(1)
	}

	fmt.Println(line)

	// Output GODEBUG default as a separate line for the link hook to parse.
	// The linker embeds this via -X=runtime.godebugDefault=<value>.
	if godebug := buildinfo.DefaultGODEBUG(moduleRoot); godebug != "" {
		fmt.Printf("godebug %s\n", godebug)
	}
}
