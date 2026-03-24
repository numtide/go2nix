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
	manifest := fs.String("manifest", "", "path to compile-manifest.json (default mode)")
	importCfg := fs.String("import-cfg", "", "path to importcfg file (dynamic mode, alternative to --manifest)")
	importPath := fs.String("import-path", "", "Go import path for the package")
	srcDir := fs.String("src-dir", "", "directory containing source files")
	output := fs.String("output", "", "output .a archive path")
	importcfgOutput := fs.String("importcfg-output", "", "write importcfg entry for consumers to this path")
	trimPath := fs.String("trim-path", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	pFlag := fs.String("p", "", "override -p flag (default: import-path)")
	goVersion := fs.String("go-version", "", "Go language version for -lang (e.g., 1.21); auto-detected from go.mod if empty")
	tags := fs.String("tags", "", "comma-separated build tags (dynamic mode)")
	gcFlags := fs.String("gc-flags", "", "space-separated extra flags for go tool compile (dynamic mode)")
	pgoProfile := fs.String("pgo-profile", "", "path to pprof CPU profile for PGO (dynamic mode)")
	_ = fs.Parse(args)

	if *importPath == "" || *srcDir == "" || *output == "" {
		slog.Error("usage: go2nix compile-package (--manifest FILE | --import-cfg FILE) --import-path PATH --src-dir DIR --output FILE [--importcfg-output FILE]")
		os.Exit(1)
	}
	if *manifest == "" && *importCfg == "" {
		slog.Error("compile-package: either --manifest or --import-cfg is required")
		os.Exit(1)
	}

	var (
		cfgPath    string
		tagsStr    string
		gcFlagList []string
		pgo        string
	)

	if *manifest != "" {
		// Default mode: load manifest for importcfg parts, tags, gcflags, PGO.
		m, err := compile.LoadCompileManifest(*manifest)
		if err != nil {
			slog.Error("compile-package: failed to load manifest", "err", err)
			os.Exit(1)
		}

		tmpDir := os.Getenv("NIX_BUILD_TOP")
		if tmpDir == "" {
			tmpDir = os.TempDir()
		}
		mergedCfg, err := compile.MergeImportcfg(m.ImportcfgParts, tmpDir)
		if err != nil {
			slog.Error("compile-package: failed to merge importcfg", "err", err)
			os.Exit(1)
		}
		cfgPath = mergedCfg
		tagsStr = strings.Join(m.Tags, ",")
		gcFlagList = m.GCFlags
		if m.PGOProfile != nil {
			pgo = *m.PGOProfile
		}
	} else {
		// Dynamic mode: importcfg, tags, gcflags, PGO passed directly via flags.
		cfgPath = *importCfg
		tagsStr = *tags
		if *gcFlags != "" {
			gcFlagList = strings.Fields(*gcFlags)
		}
		pgo = *pgoProfile
	}

	opts := compile.Options{
		ImportPath:  *importPath,
		PFlag:       *pFlag,
		SrcDir:      *srcDir,
		Output:      *output,
		ImportCfg:   cfgPath,
		TrimPath:    *trimPath,
		Tags:        tagsStr,
		GCFlagsList: gcFlagList,
		GoVersion:   *goVersion,
		PGOProfile:  pgo,
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
