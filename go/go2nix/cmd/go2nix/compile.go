package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/numtide/go2nix/pkg/compile"
)

func runCompilePackageCmd(args []string) {
	fs := flag.NewFlagSet("compile-package", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to compile-manifest.json (when set, manifest fields are authoritative)")
	importPath := fs.String("import-path", "", "Go import path for the package")
	srcDir := fs.String("src-dir", "", "directory containing source files")
	output := fs.String("output", "", "output .a archive path")
	importcfgOutput := fs.String("importcfg-output", "", "write importcfg entry for consumers to this path")
	importCfg := fs.String("import-cfg", "", "path to importcfg file (legacy, ignored when --manifest is set)")
	tags := fs.String("tags", "", "comma-separated build tags (legacy, ignored when --manifest is set)")
	gcflags := fs.String("gc-flags", "", "extra flags for go tool compile (legacy, ignored when --manifest is set)")
	trimPath := fs.String("trim-path", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	pFlag := fs.String("p", "", "override -p flag (default: import-path)")
	goVersion := fs.String("go-version", "", "Go language version for -lang (e.g., 1.21); auto-detected from go.mod if empty")
	pgoProfile := fs.String("pgo-profile", "", "path to pprof CPU profile for PGO (legacy, ignored when --manifest is set)")
	fs.Parse(args)

	if *importPath == "" || *srcDir == "" || *output == "" {
		slog.Error("usage: go2nix compile-package --import-path PATH --src-dir DIR --output FILE [--manifest FILE | --import-cfg FILE] [--importcfg-output FILE]")
		os.Exit(1)
	}

	var opts compile.Options

	if *manifest != "" {
		// Manifest mode: manifest fields are authoritative.
		m, err := compile.LoadCompileManifest(*manifest)
		if err != nil {
			slog.Error("compile-package: failed to load manifest", "err", err)
			os.Exit(1)
		}

		// Merge importcfg parts into a single file.
		tmpDir := os.Getenv("NIX_BUILD_TOP")
		if tmpDir == "" {
			tmpDir = os.TempDir()
		}
		mergedCfg, err := compile.MergeImportcfg(m.ImportcfgParts, tmpDir)
		if err != nil {
			slog.Error("compile-package: failed to merge importcfg", "err", err)
			os.Exit(1)
		}

		var pgo string
		if m.PGOProfile != nil {
			pgo = *m.PGOProfile
		}

		opts = compile.Options{
			ImportPath: *importPath,
			PFlag:      *pFlag,
			SrcDir:     *srcDir,
			Output:     *output,
			ImportCfg:  mergedCfg,
			TrimPath:   *trimPath,
			Tags:       strings.Join(m.Tags, ","),
			GCFlags:    strings.Join(m.GCFlags, " "),
			GoVersion:  *goVersion,
			PGOProfile: pgo,
		}
	} else {
		// Legacy mode: flags are authoritative.
		if *importCfg == "" {
			slog.Error("usage: go2nix compile-package requires --import-cfg or --manifest")
			os.Exit(1)
		}
		opts = compile.Options{
			ImportPath: *importPath,
			PFlag:      *pFlag,
			SrcDir:     *srcDir,
			Output:     *output,
			ImportCfg:  *importCfg,
			TrimPath:   *trimPath,
			Tags:       *tags,
			GCFlags:    *gcflags,
			GoVersion:  *goVersion,
			PGOProfile: *pgoProfile,
		}
	}

	if err := compile.CompileGoPackage(opts); err != nil {
		slog.Error("compile-package failed", "err", err, "pkg", *importPath)
		os.Exit(1)
	}

	// Write importcfg entry for consumers.
	if *importcfgOutput != "" {
		entry := fmt.Sprintf("packagefile %s=%s\n", *importPath, *output)
		if err := os.WriteFile(*importcfgOutput, []byte(entry), 0o644); err != nil {
			slog.Error("compile-package: failed to write importcfg-output", "err", err)
			os.Exit(1)
		}
	}
}
