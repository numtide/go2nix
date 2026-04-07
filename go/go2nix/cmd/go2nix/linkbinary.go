package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/numtide/go2nix/pkg/buildinfo"
	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/lockfile"
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

	var deps []buildinfo.ModDep
	if m.Lockfile != nil && *m.Lockfile != "" {
		lock, err := lockfile.Read(*m.Lockfile)
		if err != nil {
			return fmt.Errorf("reading lockfile: %w", err)
		}

		for modKey := range lock.Mod {
			modPath, version, ok := strings.Cut(modKey, "@")
			if !ok {
				continue
			}
			dep := buildinfo.ModDep{Path: modPath, Version: version}
			if replacePath, ok := lock.Replace[modKey]; ok {
				dep.Replace = &buildinfo.ModDep{Path: replacePath, Version: version}
			}
			deps = append(deps, dep)
		}
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

	modinfo, err := buildinfo.GenerateModinfo(m.ModuleRoot, goVersion, deps, settings)
	if err != nil {
		return fmt.Errorf("generating modinfo: %w", err)
	}

	// Step 6: Build link importcfg (compile importcfg + modinfo).
	linkCfg := filepath.Join(tmpDir, "importcfg.link")
	{
		data, err := os.ReadFile(mergedCfg)
		if err != nil {
			return err
		}
		content := string(data)
		if modinfo != "" {
			content += modinfo + "\n"
		}
		if err := os.WriteFile(linkCfg, []byte(content), 0o644); err != nil {
			return err
		}
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
// shell-style quoting rules.
//
// Nix users write ldflags like ["-X main.Version=1.6"] or
// ["-extldflags '-static -L/foo/lib'"] and expect the quoted value to stay
// intact when invoking the linker directly.
func expandLDFlags(flags []string) ([]string, error) {
	var out []string
	for _, f := range flags {
		parts, err := splitShellFields(f)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

func splitShellFields(s string) ([]string, error) {
	var (
		out          []string
		token        strings.Builder
		quote        rune
		escaped      bool
		tokenStarted bool
	)

	flush := func() {
		if !tokenStarted {
			return
		}
		out = append(out, token.String())
		token.Reset()
		tokenStarted = false
	}

	for _, r := range s {
		if escaped {
			token.WriteRune(r)
			escaped = false
			continue
		}

		switch quote {
		case '\'':
			if r == '\'' {
				quote = 0
				continue
			}
			token.WriteRune(r)
		case '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				token.WriteRune(r)
			}
		default:
			switch {
			case unicode.IsSpace(r):
				flush()
			case r == '\'' || r == '"':
				tokenStarted = true
				quote = r
			case r == '\\':
				tokenStarted = true
				escaped = true
			default:
				tokenStarted = true
				token.WriteRune(r)
			}
		}
	}

	if escaped {
		return nil, fmt.Errorf("unterminated escape sequence")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted string")
	}

	flush()
	return out, nil
}
