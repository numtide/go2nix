package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/testrunner"
)

func runTestPackagesCmd(args []string) {
	fs := flag.NewFlagSet("test-packages", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to test-manifest.json (required)")
	_ = fs.Parse(args)

	if *manifest == "" {
		slog.Error("usage: go2nix test-packages --manifest FILE")
		os.Exit(1)
	}

	m, err := compile.LoadTestManifest(*manifest)
	if err != nil {
		slog.Error("test-packages: failed to load manifest", "err", err)
		os.Exit(1)
	}

	// Merge importcfg parts.
	tmpDir := os.Getenv("NIX_BUILD_TOP")
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	mergedCfg, err := compile.MergeImportcfg(m.ImportcfgParts, tmpDir)
	if err != nil {
		slog.Error("test-packages: failed to merge importcfg", "err", err)
		os.Exit(1)
	}

	// Reconstruct local-pkgs directory from manifest's localArchives map.
	localPkgsDir := filepath.Join(tmpDir, "local-pkgs")
	for importPath, archivePath := range m.LocalArchives {
		dir := filepath.Join(localPkgsDir, filepath.Dir(importPath))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Error("test-packages: failed to create local-pkgs dir", "err", err)
			os.Exit(1)
		}
		dst := filepath.Join(localPkgsDir, importPath+".a")
		if err := os.Symlink(archivePath, dst); err != nil && !os.IsExist(err) {
			slog.Error("test-packages: failed to symlink local archive", "err", err, "path", importPath)
			os.Exit(1)
		}
	}

	opts := testrunner.Options{
		ModuleRoot:     m.ModuleRoot,
		ImportCfg:      mergedCfg,
		LocalDir:       localPkgsDir,
		TrimPath:       tmpDir,
		Tags:           strings.Join(m.Tags, ","),
		GCFlagsList:    m.GCFlags,
		CheckFlagsList: m.CheckFlags,
	}

	if err := testrunner.Run(opts); err != nil {
		slog.Error("test-packages failed", "err", err)
		os.Exit(1)
	}
}
