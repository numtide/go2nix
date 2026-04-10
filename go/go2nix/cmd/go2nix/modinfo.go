package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/numtide/go2nix/pkg/buildinfo"
	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/lockfile"
)

func runModinfoCmd(args []string) {
	fs := flag.NewFlagSet("build-modinfo", flag.ExitOnError)
	lockfilePath := fs.String("lockfile", "", "path to go2nix.toml lockfile")
	goBin := fs.String("go", "", "path to go binary (default: from PATH)")
	mainPath := fs.String("main-path", "", "import path of the main package (default: module path)")
	mainDir := fs.String("main-dir", "", "directory of the main package, for //go:debug directives (default: MODULE_ROOT)")
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

	srcDir := *mainDir
	if srcDir == "" {
		srcDir = moduleRoot
	}
	srcDirectives := buildinfo.ParseSourceGodebugs(nil, srcDir)
	godebug := buildinfo.DefaultGODEBUG(moduleRoot, srcDirectives)

	goos := compile.GoEnvVar("GOOS")
	goarch := compile.GoEnvVar("GOARCH")
	settings := buildinfo.BuildSettings{
		BuildMode:      compile.DefaultBuildMode(goos, goarch),
		DefaultGODEBUG: godebug,
		CGOEnabled:     compile.GoEnvVar("CGO_ENABLED"),
		GOARCH:         goarch,
		GOOS:           goos,
	}
	if key := buildinfo.ArchLevelVar(goarch); key != "" {
		settings.GOARCHLevel = compile.GoEnvVar(key)
	}

	line, err := buildinfo.GenerateModinfo(moduleRoot, *mainPath, goVersion, deps, settings)
	if err != nil {
		slog.Error("generating modinfo", "err", err)
		os.Exit(1)
	}

	fmt.Println(line)

	// Output GODEBUG default as a separate line for the link hook to parse.
	// The linker embeds this via -X=runtime.godebugDefault=<value>.
	if godebug != "" {
		fmt.Printf("godebug %s\n", godebug)
	}
}
