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
	manifest := fs.String("manifest", "", "path to compile-manifest.json (required)")
	importPath := fs.String("import-path", "", "Go import path for the package")
	srcDir := fs.String("src-dir", "", "directory containing source files")
	output := fs.String("output", "", "output .a archive path")
	ifaceOutput := fs.String("iface-output", "", "optional export-data-only interface output path; when set, --output receives the link object via -linkobj")
	importcfgOutput := fs.String("importcfg-output", "", "write importcfg entry for consumers to this path")
	trimPath := fs.String("trim-path", "", "path prefix to trim (default: $NIX_BUILD_TOP)")
	pFlag := fs.String("p", "", "override -p flag (default: import-path)")
	goVersion := fs.String("go-version", "", "Go language version for -lang (e.g., 1.21); auto-detected from go.mod if empty")
	_ = fs.Parse(args)

	if *manifest == "" || *importPath == "" || *srcDir == "" || *output == "" {
		slog.Error("usage: go2nix compile-package --manifest FILE --import-path PATH --src-dir DIR --output FILE [--importcfg-output FILE]")
		os.Exit(1)
	}

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

	opts := compile.Options{
		ImportPath:  *importPath,
		PFlag:       *pFlag,
		SrcDir:      *srcDir,
		Output:      *output,
		IfaceOutput: *ifaceOutput,
		ImportCfg:   mergedCfg,
		TrimPath:    *trimPath,
		Tags:        strings.Join(m.Tags, ","),
		GCFlagsList: m.GCFlags,
		GoVersion:   *goVersion,
		PGOProfile:  pgo,
	}

	if err := compile.CompileGoPackage(opts); err != nil {
		slog.Error("compile-package failed", "err", err, "pkg", *importPath)
		os.Exit(1)
	}

	// Write importcfg entry for downstream consumers. When the interface
	// split is on, downstream compiles must read the export-data-only file
	// so their inputs don't change when only the link object does.
	if *importcfgOutput != "" {
		target := *output
		if *ifaceOutput != "" {
			target = *ifaceOutput
		}
		entry := fmt.Sprintf("packagefile %s=%s\n", *importPath, target)
		if err := os.WriteFile(*importcfgOutput, []byte(entry), 0o644); err != nil {
			slog.Error("compile-package: failed to write importcfg-output", "err", err)
			os.Exit(1)
		}
	}
}
