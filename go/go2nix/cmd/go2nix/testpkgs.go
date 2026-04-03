package main

import (
	"flag"
	"fmt"
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

	// buildCfg merges importcfg parts and appends per-importpath local
	// entries (third-party are already in the merged parts). The
	// resulting file is the link-side cfg by default; with iface-split
	// fields populated we also build a separate compile-side cfg whose
	// local entries point at .x export-data files.
	buildCfg := func(name string, parts []string, locals map[string]string) (string, error) {
		dir := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating %s dir: %w", name, err)
		}
		cfg, err := compile.MergeImportcfg(parts, dir)
		if err != nil {
			return "", fmt.Errorf("merging %s: %w", name, err)
		}
		f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return "", fmt.Errorf("opening %s: %w", name, err)
		}
		for importPath, p := range locals {
			if _, err := fmt.Fprintf(f, "packagefile %s=%s\n", importPath, p); err != nil {
				_ = f.Close()
				return "", fmt.Errorf("writing %s entry: %w", name, err)
			}
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("closing %s: %w", name, err)
		}
		return cfg, nil
	}

	mergedCfg, err := buildCfg("link-cfg", m.ImportcfgParts, m.LocalArchives)
	if err != nil {
		slog.Error("test-packages: failed to build link importcfg", "err", err)
		os.Exit(1)
	}

	compileCfg := ""
	if len(m.CompileImportcfgParts) > 0 {
		compileCfg, err = buildCfg("compile-cfg", m.CompileImportcfgParts, m.LocalIfaces)
		if err != nil {
			slog.Error("test-packages: failed to build compile importcfg", "err", err)
			os.Exit(1)
		}
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
		ModuleRoot:       m.ModuleRoot,
		ImportCfg:        mergedCfg,
		CompileImportCfg: compileCfg,
		LocalDir:         localPkgsDir,
		TrimPath:         tmpDir,
		Tags:             strings.Join(m.Tags, ","),
		GCFlagsList:      m.GCFlags,
		CheckFlagsList:   m.CheckFlags,
	}

	if err := testrunner.Run(opts); err != nil {
		slog.Error("test-packages failed", "err", err)
		os.Exit(1)
	}
}
