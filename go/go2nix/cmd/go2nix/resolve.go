package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/numtide/go2nix/pkg/resolve"
)

func runResolveCmd(args []string) {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	src := fs.String("src", "", "store path to Go source")
	lockfilePath := fs.String("lockfile", "", "path to go2nix.toml lockfile")
	system := fs.String("system", "", "Nix system (e.g., x86_64-linux)")
	goBin := fs.String("go", "", "path to go binary")
	stdlibPath := fs.String("stdlib", "", "path to pre-compiled Go stdlib")
	nixBin := fs.String("nix", "", "path to nix binary")
	go2nixBin := fs.String("go2nix", "", "path to go2nix binary")
	bashBin := fs.String("bash", "", "path to bash binary")
	pname := fs.String("pname", "", "output binary name")
	subPackages := fs.String("sub-packages", "", "comma-separated sub-packages")
	tags := fs.String("tags", "", "comma-separated build tags")
	ldflags := fs.String("ldflags", "", "linker flags")
	overrides := fs.String("overrides", "", "JSON-encoded packageOverrides")
	cacert := fs.String("cacert", "", "path to CA certificate bundle")
	output := fs.String("output", "", "$out path")
	fs.Parse(args)

	if *src == "" || *lockfilePath == "" || *system == "" || *goBin == "" ||
		*nixBin == "" || *pname == "" || *output == "" {
		slog.Error("usage: go2nix resolve --src PATH --lockfile PATH --system SYSTEM --go PATH --nix PATH --pname NAME --output PATH [--stdlib PATH] [--go2nix PATH] [--bash PATH] [--sub-packages PKGS] [--tags TAGS] [--ldflags FLAGS] [--overrides JSON] [--cacert PATH]")
		os.Exit(1)
	}

	// Default go2nix to our own binary
	g2n := *go2nixBin
	if g2n == "" {
		var err error
		g2n, err = os.Executable()
		if err != nil {
			slog.Error("cannot determine go2nix path", "err", err)
			os.Exit(1)
		}
	}

	// Default bash
	bash := *bashBin
	if bash == "" {
		bash = "/bin/bash"
	}

	cfg := resolve.Config{
		Src:         *src,
		LockFile:    *lockfilePath,
		System:      *system,
		GoBin:       *goBin,
		StdlibPath:  *stdlibPath,
		NixBin:      *nixBin,
		Go2NixBin:   g2n,
		BashBin:     bash,
		PName:       *pname,
		SubPackages: *subPackages,
		Tags:        *tags,
		LDFlags:     *ldflags,
		Overrides:   *overrides,
		CACert:      *cacert,
		Output:      *output,
	}

	if err := resolve.Resolve(cfg); err != nil {
		slog.Error("resolve failed", "err", err)
		os.Exit(1)
	}
}
