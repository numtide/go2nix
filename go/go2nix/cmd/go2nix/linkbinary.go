package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/buildinfo"
	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/mvscheck"
)

func runLinkBinaryCmd(args []string) {
	fs := flag.NewFlagSet("link-binary", flag.ExitOnError)
	manifestPath := fs.String("manifest", "", "path to link-manifest.json (required)")
	output := fs.String("output", "", "output directory (binaries written to <output>/bin/)")
	_ = fs.Parse(args)

	if *manifestPath == "" || *output == "" {
		slog.Error("usage: go2nix link-binary --manifest FILE --output DIR")
		os.Exit(1)
	}

	if err := linkBinary(*manifestPath, *output); err != nil {
		slog.Error("link-binary failed", "err", err)
		os.Exit(1)
	}
}

func linkBinary(manifestPath, output string) error {
	m, err := compile.LoadLinkManifest(manifestPath)
	if err != nil {
		return err
	}

	tmpDir := os.Getenv("NIX_BUILD_TOP")
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}

	// Step 1: Validate lockfile consistency (skipped in lockfile-free mode).
	if m.Lockfile != nil && *m.Lockfile != "" {
		if err := mvscheck.CheckLockfile(m.ModuleRoot, *m.Lockfile); err != nil {
			return fmt.Errorf("lockfile check: %w", err)
		}
	}

	// Step 2: Extract module path from go.mod.
	modulePath, err := extractModulePath(m.ModuleRoot)
	if err != nil {
		return err
	}

	// Step 3: Merge importcfg parts.
	buildCfg := func(name string, parts []string, locals map[string]string) (string, error) {
		dir := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
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
		return err
	}

	// In interface-split mode, the main-package compile reads export-data
	// (.x) files so private-symbol changes upstream don't invalidate it.
	// The link step always reads mergedCfg (.a link objects).
	compileCfg := mergedCfg
	if len(m.CompileImportcfgParts) > 0 {
		compileCfg, err = buildCfg("compile-cfg", m.CompileImportcfgParts, m.LocalIfaces)
		if err != nil {
			return err
		}
	}

	// Step 4: Determine target platform and build mode (needed for modinfo
	// build settings as well as the link step).
	goos := ""
	goarch := ""
	if m.GOOS != nil {
		goos = *m.GOOS
	}
	if m.GOARCH != nil {
		goarch = *m.GOARCH
	}
	if goos == "" {
		goos = compile.GoEnvVar("GOOS")
	}
	if goarch == "" {
		goarch = compile.GoEnvVar("GOARCH")
	}
	buildMode := compile.DefaultBuildMode(goos, goarch)

	// Step 5: Compute modinfo.
	goVersion, err := goToolchainVersion()
	if err != nil {
		return fmt.Errorf("getting Go version: %w", err)
	}

	godebugDefault := buildinfo.DefaultGODEBUG(m.ModuleRoot)

	settings := buildinfo.BuildSettings{
		BuildMode:      buildMode,
		LDFlags:        strings.Join(m.LDFlags, " "),
		Tags:           strings.Join(m.Tags, ","),
		DefaultGODEBUG: godebugDefault,
		CGOEnabled:     compile.GoEnvVar("CGO_ENABLED"),
		GOARCH:         goarch,
		GOOS:           goos,
	}
	if key := buildinfo.ArchLevelVar(goarch); key != "" {
		settings.GOARCHLevel = compile.GoEnvVar(key)
	}

	// Step 6: Read merged importcfg once; the per-binary modinfo line (which
	// carries BuildInfo.Path = main package import path) is appended inside
	// the SubPackages loop so each binary records its own path.
	mergedCfgData, err := os.ReadFile(mergedCfg)
	if err != nil {
		return err
	}
	linkCfgDir := filepath.Join(tmpDir, "linkcfg")
	if err := os.MkdirAll(linkCfgDir, 0o755); err != nil {
		return err
	}

	// Build gcflags: PIE requires -shared, then append manifest gcflags.
	var gcflagsList []string
	if buildMode == "pie" {
		gcflagsList = append(gcflagsList, "-shared")
	}
	gcflagsList = append(gcflagsList, m.GCFlags...)

	var pgoProfile string
	if m.PGOProfile != nil {
		pgoProfile = *m.PGOProfile
	}

	// Resolve the linker binary before clearing GOROOT.
	goToolDir, err := exec.Command("go", "env", "GOTOOLDIR").Output()
	if err != nil {
		return fmt.Errorf("go env GOTOOLDIR: %w", err)
	}
	goLinkTool := filepath.Join(strings.TrimSpace(string(goToolDir)), "link")

	// Do not set GOROOT: the linker reads it from os.Getenv and embeds it
	// as runtime.defaultGOROOT. Invoking the linker directly matches what
	// `go build -trimpath` does internally.
	_ = os.Setenv("GOROOT", "")

	// Step 7: Compile main packages and link.
	mainDir := filepath.Join(tmpDir, "main-pkgs")
	binDir := filepath.Join(output, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}

	for _, sp := range m.SubPackages {
		// compileCgo writes .has_cgo / .has_cxx into NIX_BUILD_TOP; clear any
		// markers left by the previous subpackage so a pure-Go binary built
		// after a cgo one doesn't inherit -extld / -linkmode external.
		_ = os.Remove(filepath.Join(tmpDir, ".has_cgo"))
		_ = os.Remove(filepath.Join(tmpDir, ".has_cxx"))

		deps := make([]buildinfo.ModDep, 0, len(sp.Modules))
		for _, mm := range sp.Modules {
			dep := buildinfo.ModDep{Path: mm.Path, Version: mm.Version}
			if mm.ReplacePath != "" {
				dep.Replace = &buildinfo.ModDep{Path: mm.ReplacePath, Version: mm.ReplaceVersion}
			}
			deps = append(deps, dep)
		}

		var importpath, srcdir, binname string

		clean := strings.TrimPrefix(sp.Path, "./")
		if sp.Path == "." || clean == "" {
			importpath = modulePath
			srcdir = m.ModuleRoot
			binname = m.Pname
			if binname == "" {
				binname = filepath.Base(modulePath)
			}
		} else {
			importpath = modulePath + "/" + clean
			srcdir = filepath.Join(m.ModuleRoot, clean)
			binname = filepath.Base(clean)
		}

		slog.Info("compiling main", "pkg", importpath)

		mainArchive := filepath.Join(mainDir, importpath+".a")
		if err := os.MkdirAll(filepath.Dir(mainArchive), 0o755); err != nil {
			return err
		}

		if sp.Files == nil {
			return fmt.Errorf("link manifest: subPackage %q is missing files; rebuild the nix-plugin", sp.Path)
		}
		pf, err := sp.Files.ToPkgFiles(srcdir)
		if err != nil {
			return fmt.Errorf("resolving files for %s: %w", importpath, err)
		}

		if err := compile.CompileGoPackage(compile.Options{
			ImportPath:  "main",
			SrcDir:      srcdir,
			Output:      mainArchive,
			ImportCfg:   compileCfg,
			TrimPath:    tmpDir,
			GCFlagsList: gcflagsList,
			Tags:        m.Tags,
			PGOProfile:  pgoProfile,
			Files:       pf,
		}); err != nil {
			return fmt.Errorf("compiling main %s: %w", importpath, err)
		}

		// Detect CGO from compilation.
		hasCgo := fileExists(filepath.Join(tmpDir, ".has_cgo"))
		hasCxx := fileExists(filepath.Join(tmpDir, ".has_cxx"))

		// Compute link flags.
		var linkFlags []string
		if hasCgo {
			extld := os.Getenv("CC")
			if hasCxx {
				extld = os.Getenv("CXX")
			}
			if extld == "" {
				return fmt.Errorf("cgo package requires CC (or CXX) but none is set")
			}
			linkFlags = append(linkFlags, "-extld", extld, "-linkmode", "external")
		}

		// Propagate sanitizer flags from gcflags to linker.
		for _, flag := range m.GCFlags {
			switch flag {
			case "-race", "-msan", "-asan":
				linkFlags = append(linkFlags, flag)
			}
		}

		// GODEBUG default.
		if godebugDefault != "" {
			linkFlags = append(linkFlags, "-X=runtime.godebugDefault="+godebugDefault)
		}

		// Per-binary modinfo: BuildInfo.Path must be the main package's
		// import path (matching `go build`), so each subPackage gets its
		// own importcfg.link.
		modinfo, err := buildinfo.GenerateModinfo(m.ModuleRoot, importpath, goVersion, deps, settings)
		if err != nil {
			return fmt.Errorf("generating modinfo for %s: %w", importpath, err)
		}
		linkCfg := filepath.Join(linkCfgDir, binname+".importcfg.link")
		if err := os.WriteFile(linkCfg, append(mergedCfgData, []byte(modinfo+"\n")...), 0o644); err != nil {
			return err
		}

		// Assemble linker command.
		linkArgs := []string{
			"-buildid=redacted",
			"-buildmode=" + buildMode,
			"-importcfg", linkCfg,
		}
		ldflags, err := expandLDFlags(m.LDFlags)
		if err != nil {
			return fmt.Errorf("parsing ldflags for %s: %w", importpath, err)
		}
		linkArgs = append(linkArgs, ldflags...)
		linkArgs = append(linkArgs, linkFlags...)
		linkArgs = append(linkArgs, "-o", filepath.Join(binDir, binname))
		linkArgs = append(linkArgs, mainArchive)

		slog.Info("linking", "pkg", importpath, "bin", binname)
		cmd := exec.Command(goLinkTool, linkArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("linking %s: %w", importpath, err)
		}
	}

	return nil
}

func extractModulePath(moduleRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("could not extract module path from %s/go.mod", moduleRoot)
}

func goToolchainVersion() (string, error) {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// expandLDFlags splits each ldflag element into separate exec arguments using
// the same quoting rules as `go build -ldflags` (cmd/internal/quoted.Split).
//
// Nix users write ldflags like ["-X main.Version=1.6"] or
// ["-extldflags '-static -L/foo/lib'"] and expect the quoted value to stay
// intact when invoking the linker directly.
func expandLDFlags(flags []string) ([]string, error) {
	var out []string
	for _, f := range flags {
		parts, err := quotedSplit(f)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

// quotedSplit is copied from src/cmd/internal/quoted/quoted.go (Go 1.26.1,
// BSD-3-Clause) so go2nix's ldflags parsing matches go build -ldflags exactly.
// Upstream ships an identical copy in cmd/dist/quoted.go for the same reason.
//
// Split splits s into a list of fields, allowing single or double quotes
// around elements. There is no unescaping or other processing within
// quoted fields.
func quotedSplit(s string) ([]string, error) {
	// Split fields allowing '' or "" around elements.
	// Quotes further inside the string do not count.
	var f []string
	for len(s) > 0 {
		for len(s) > 0 && isSpaceByte(s[0]) {
			s = s[1:]
		}
		if len(s) == 0 {
			break
		}
		// Accepted quoted string. No unescaping inside.
		if s[0] == '"' || s[0] == '\'' {
			quote := s[0]
			s = s[1:]
			i := 0
			for i < len(s) && s[i] != quote {
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated %c string", quote)
			}
			f = append(f, s[:i])
			s = s[i+1:]
			continue
		}
		i := 0
		for i < len(s) && !isSpaceByte(s[i]) {
			i++
		}
		f = append(f, s[:i])
		s = s[i:]
	}
	return f, nil
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
