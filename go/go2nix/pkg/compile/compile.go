// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/numtide/go2nix/pkg/gofiles"
)

// Options configures a package compilation.
type Options struct {
	ImportPath string // Go import path (e.g., "github.com/foo/bar")
	PFlag      string // -p flag for go tool compile (defaults to ImportPath)
	SrcDir     string // directory containing source files
	Output     string // output .a archive path
	ImportCfg  string // path to importcfg file
	TrimPath   string // path prefix to trim (defaults to $NIX_BUILD_TOP)
	Tags       string // comma-separated build tags
}

// CompilePackage compiles a single Go package (pure Go, assembly, or cgo).
func CompilePackage(opts Options) error {
	if opts.PFlag == "" {
		opts.PFlag = opts.ImportPath
	}
	if opts.TrimPath == "" {
		opts.TrimPath = os.Getenv("NIX_BUILD_TOP")
	}

	slog.Debug("compile-package", "import-path", opts.ImportPath, "src", opts.SrcDir)

	files, err := gofiles.ListFiles(opts.SrcDir, opts.Tags)
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}

	if len(files.GoFiles) == 0 && len(files.CgoFiles) == 0 {
		return fmt.Errorf("no Go files found in %s (package %s)", opts.SrcDir, opts.ImportPath)
	}

	// Create output directory.
	if err := os.MkdirAll(filepath.Dir(opts.Output), 0o755); err != nil {
		return err
	}

	// Write embedcfg if needed.
	var embedFlag string
	if files.EmbedCfg != nil {
		uid := strings.ReplaceAll(opts.ImportPath, "/", "_")
		embedPath := filepath.Join(opts.TrimPath, "embedcfg_"+uid+".json")
		f, err := os.Create(embedPath)
		if err != nil {
			return fmt.Errorf("creating embedcfg: %w", err)
		}
		if err := json.NewEncoder(f).Encode(files.EmbedCfg); err != nil {
			f.Close()
			return fmt.Errorf("writing embedcfg: %w", err)
		}
		f.Close()
		embedFlag = "-embedcfg=" + embedPath
	}

	if len(files.CgoFiles) > 0 {
		return compileCgo(opts, files, embedFlag)
	}
	if len(files.SFiles) > 0 {
		return compileWithAsm(opts, files, embedFlag)
	}
	return compilePureGo(opts, files, embedFlag)
}

func compilePureGo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	args := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
		"-trimpath=" + opts.TrimPath,
		"-pack",
		"-o", opts.Output,
	}
	if embedFlag != "" {
		args = append(args, embedFlag)
	}
	args = append(args, files.GoFiles...)

	return runIn(opts.SrcDir, "go", args...)
}

func compileWithAsm(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	uid := strings.ReplaceAll(opts.ImportPath, "/", "_")

	// go_asm.h must be named exactly "go_asm.h" — assembly files #include it.
	asmhdr := filepath.Join(opts.TrimPath, "go_asm.h")
	if err := os.WriteFile(asmhdr, nil, 0o644); err != nil {
		return err
	}

	goroot, err := goRoot()
	if err != nil {
		return err
	}
	goOS, goArch := goEnv()

	// Pass 1: generate symabis.
	symabis := filepath.Join(opts.TrimPath, "symabis_"+uid)
	asmArgs := []string{
		"tool", "asm",
		"-p", opts.PFlag,
		"-trimpath", opts.TrimPath,
		"-I", opts.TrimPath,
		"-I", filepath.Join(goroot, "pkg", "include"),
		"-D", "GOOS_" + goOS, "-D", "GOARCH_" + goArch,
		"-gensymabis",
		"-o", symabis,
	}
	asmArgs = append(asmArgs, files.SFiles...)
	if err := runIn(opts.SrcDir, "go", asmArgs...); err != nil {
		return fmt.Errorf("gensymabis: %w", err)
	}

	// Pass 2: compile Go with symabis + asmhdr.
	compileArgs := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
		"-trimpath=" + opts.TrimPath,
		"-symabis", symabis,
		"-asmhdr", asmhdr,
		"-pack",
		"-o", opts.Output,
	}
	if embedFlag != "" {
		compileArgs = append(compileArgs, embedFlag)
	}
	compileArgs = append(compileArgs, files.GoFiles...)
	if err := runIn(opts.SrcDir, "go", compileArgs...); err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	// Pass 3: assemble each .s file and pack.
	for _, sf := range files.SFiles {
		base := strings.TrimSuffix(sf, ".s")
		objFile := filepath.Join(opts.TrimPath, base+"_"+uid+".o")
		asmFileArgs := []string{
			"tool", "asm",
			"-p", opts.PFlag,
			"-trimpath", opts.TrimPath,
			"-I", opts.TrimPath,
			"-I", filepath.Join(goroot, "pkg", "include"),
			"-D", "GOOS_" + goOS, "-D", "GOARCH_" + goArch,
			"-o", objFile,
			sf,
		}
		if err := runIn(opts.SrcDir, "go", asmFileArgs...); err != nil {
			return fmt.Errorf("asm %s: %w", sf, err)
		}
		if err := runIn(opts.SrcDir, "go", "tool", "pack", "r", opts.Output, objFile); err != nil {
			return fmt.Errorf("pack %s: %w", sf, err)
		}
	}

	return nil
}

func compileCgo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	uid := strings.ReplaceAll(opts.ImportPath, "/", "_")

	// Signal that cgo was used (linker needs -extld).
	if nixBuildTop := os.Getenv("NIX_BUILD_TOP"); nixBuildTop != "" {
		os.WriteFile(filepath.Join(nixBuildTop, ".has_cgo"), nil, 0o644)
	}

	cgowork, err := os.MkdirTemp(opts.TrimPath, "cgo_work_"+uid+"_")
	if err != nil {
		return err
	}

	cc := envOrDefault("CC", "gcc")
	cxx := envOrDefault("CXX", "g++")

	// Read CGO flags from environment (Nix CC wrapper sets these when C libs are in nativeBuildInputs).
	cgoCflags := strings.Fields(os.Getenv("CGO_CFLAGS"))
	cgoCxxflags := strings.Fields(os.Getenv("CGO_CXXFLAGS"))
	cgoLdflags := strings.Fields(os.Getenv("CGO_LDFLAGS"))

	// Resolve #cgo pkg-config: directives from source files.
	// go tool cgo does not process pkg-config directives; that's done by cmd/go.
	// We handle it here so packages with #cgo pkg-config: work correctly.
	pkgCflags, pkgLdflags, err := resolvePkgConfig(opts.SrcDir, files.CgoFiles)
	if err != nil {
		return fmt.Errorf("pkg-config: %w", err)
	}
	cgoCflags = append(cgoCflags, pkgCflags...)
	cgoLdflags = append(cgoLdflags, pkgLdflags...)

	// Filter C++ standard library flags when no C++ sources are present.
	if len(files.CXXFiles) == 0 {
		cgoLdflags = filterCppFlags(cgoLdflags)
	}

	// Ensure absolute build paths don't leak into debug info for reproducibility.
	cgoCflags = append([]string{"-fdebug-prefix-map=" + opts.SrcDir + "=."}, cgoCflags...)

	// Copy headers.
	for _, h := range files.HFiles {
		data, err := os.ReadFile(filepath.Join(opts.SrcDir, h))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cgowork, h), data, 0o644); err != nil {
			return err
		}
	}

	// Step 1: go tool cgo.
	// Pass CGO_CFLAGS after "--" so cgo's internal C compiler picks them up
	// (needed for #cgo pkg-config: directives and explicit -I/-L flags).
	cgoArgs := []string{
		"tool", "cgo",
		"-objdir", cgowork,
		"-importpath", opts.PFlag,
		"--",
		"-I", cgowork,
	}
	cgoArgs = append(cgoArgs, cgoCflags...)
	cgoArgs = append(cgoArgs, files.CgoFiles...)
	if err := runIn(opts.SrcDir, "go", cgoArgs...); err != nil {
		return fmt.Errorf("cgo: %w", err)
	}

	// Read _cgo_flags written by go tool cgo (contains LDFLAGS from #cgo directives).
	var cgoFlagsLDFLAGS []string
	cgoFlagsFile := filepath.Join(cgowork, "_cgo_flags")
	if data, err := os.ReadFile(cgoFlagsFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if after, ok := strings.CutPrefix(line, "_CGO_LDFLAGS="); ok {
				cgoFlagsLDFLAGS = strings.Fields(after)
			}
		}
	}

	// Append pkg-config LDFLAGS to _cgo_flags so they propagate to the final
	// link step via the packed archive.
	if len(pkgLdflags) > 0 {
		cgoFlagsLDFLAGS = append(cgoFlagsLDFLAGS, pkgLdflags...)
		allFlags := strings.Join(cgoFlagsLDFLAGS, " ")
		os.WriteFile(cgoFlagsFile, []byte("_CGO_LDFLAGS="+allFlags+"\n"), 0o644)
	}

	// Step 2: compile C files.
	var compiledOFiles []string

	// Collect cgo-generated C files.
	var ccFiles []string
	cgoExport := filepath.Join(cgowork, "_cgo_export.c")
	if _, err := os.Stat(cgoExport); err == nil {
		ccFiles = append(ccFiles, cgoExport)
	}
	cgo2Files, _ := filepath.Glob(filepath.Join(cgowork, "*.cgo2.c"))
	ccFiles = append(ccFiles, cgo2Files...)
	// User C files.
	for _, f := range files.CFiles {
		ccFiles = append(ccFiles, filepath.Join(opts.SrcDir, f))
	}

	for _, f := range ccFiles {
		base := strings.TrimSuffix(filepath.Base(f), ".c")
		oFile := filepath.Join(cgowork, base+"_"+uid+".o")
		ccArgs := []string{"-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread"}
		ccArgs = append(ccArgs, cgoCflags...)
		ccArgs = append(ccArgs, f, "-o", oFile)
		if err := run(cc, ccArgs...); err != nil {
			return fmt.Errorf("cc %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	// Compile .S files with C preprocessor.
	for _, f := range files.SFiles {
		base := strings.TrimSuffix(f, ".S")
		oFile := filepath.Join(cgowork, base+"_asm_"+uid+".o")
		sArgs := []string{"-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread"}
		sArgs = append(sArgs, cgoCflags...)
		sArgs = append(sArgs, filepath.Join(opts.SrcDir, f), "-o", oFile)
		if err := run(cc, sArgs...); err != nil {
			return fmt.Errorf("cc asm %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	// Compile C++ files.
	for _, f := range files.CXXFiles {
		base := filepath.Base(f)
		base = strings.TrimSuffix(base, ".cc")
		base = strings.TrimSuffix(base, ".cpp")
		base = strings.TrimSuffix(base, ".cxx")
		oFile := filepath.Join(cgowork, base+"_cxx_"+uid+".o")
		cxxArgs := []string{"-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread"}
		cxxArgs = append(cxxArgs, cgoCxxflags...)
		cxxArgs = append(cxxArgs, filepath.Join(opts.SrcDir, f), "-o", oFile)
		if err := run(cxx, cxxArgs...); err != nil {
			return fmt.Errorf("cxx %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	// Step 3: test link + dynimport.
	cgoMainC := filepath.Join(cgowork, "_cgo_main.c")
	if _, err := os.Stat(cgoMainC); err == nil {
		mainO := filepath.Join(cgowork, "_cgo_main_"+uid+".o")
		mainArgs := []string{"-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread"}
		mainArgs = append(mainArgs, cgoCflags...)
		mainArgs = append(mainArgs, cgoMainC, "-o", mainO)
		_ = run(cc, mainArgs...)

		testLinkO := filepath.Join(cgowork, "_cgo__"+uid+".o")
		linkArgs := append([]string{"-o", testLinkO, mainO}, compiledOFiles...)
		linkArgs = append(linkArgs, "-lpthread")
		linkArgs = append(linkArgs, cgoFlagsLDFLAGS...)
		linkArgs = append(linkArgs, cgoLdflags...)
		if err := run(cc, linkArgs...); err != nil {
			// Test link may fail due to unresolved external symbols.
			// Retry allowing unresolved symbols since this binary is
			// only used to extract dynamic imports.
			goos, _ := goEnv()
			var flag string
			switch goos {
			case "darwin", "ios":
				flag = "-Wl,-undefined,dynamic_lookup"
			default:
				flag = "-Wl,--unresolved-symbols=ignore-all"
			}
			if err2 := run(cc, append(linkArgs, flag)...); err2 != nil {
				slog.Debug("cgo test link failed (no dynamic imports)", "err", err)
			}
		}
		if _, err := os.Stat(testLinkO); err == nil {
			// Extract package name from generated Go file.
			pkgName := extractPackageName(filepath.Join(cgowork, "_cgo_gotypes.go"))
			dynOut := filepath.Join(cgowork, "_cgo_import_"+uid+".go")
			if err := runIn(opts.SrcDir, "go", "tool", "cgo",
				"-dynimport", testLinkO,
				"-dynout", dynOut,
				"-dynpackage", pkgName,
				"-dynlinker"); err != nil {
				slog.Debug("cgo dynimport failed", "err", err)
			}
		}
	}

	// Generate //go:cgo_ldflag directives so the linker picks up LDFLAGS.
	// Normally cmd/go does this, but we invoke go tool cgo directly.
	allLdflags := append(append([]string{}, cgoFlagsLDFLAGS...), cgoLdflags...)
	if len(allLdflags) > 0 {
		pkgName := extractPackageName(filepath.Join(cgowork, "_cgo_gotypes.go"))
		ldflagFile := filepath.Join(cgowork, "_cgo_ldflag_"+uid+".go")
		var sb strings.Builder
		fmt.Fprintf(&sb, "package %s\n\n", pkgName)
		for _, flag := range allLdflags {
			fmt.Fprintf(&sb, "//go:cgo_ldflag %q\n", flag)
		}
		if err := os.WriteFile(ldflagFile, []byte(sb.String()), 0o644); err != nil {
			return fmt.Errorf("writing cgo_ldflag: %w", err)
		}
	}

	// Step 4: compile Go + cgo-generated Go files.
	var cgoGoFiles []string
	gotypes := filepath.Join(cgowork, "_cgo_gotypes.go")
	if _, err := os.Stat(gotypes); err == nil {
		cgoGoFiles = append(cgoGoFiles, gotypes)
	}
	cgo1Files, _ := filepath.Glob(filepath.Join(cgowork, "*.cgo1.go"))
	cgoGoFiles = append(cgoGoFiles, cgo1Files...)
	dynImport := filepath.Join(cgowork, "_cgo_import_"+uid+".go")
	if _, err := os.Stat(dynImport); err == nil {
		cgoGoFiles = append(cgoGoFiles, dynImport)
	}
	ldflagFile := filepath.Join(cgowork, "_cgo_ldflag_"+uid+".go")
	if _, err := os.Stat(ldflagFile); err == nil {
		cgoGoFiles = append(cgoGoFiles, ldflagFile)
	}

	compileArgs := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
		"-trimpath=" + opts.TrimPath,
		"-pack",
		"-o", opts.Output,
	}
	if embedFlag != "" {
		compileArgs = append(compileArgs, embedFlag)
	}
	compileArgs = append(compileArgs, files.GoFiles...)
	compileArgs = append(compileArgs, cgoGoFiles...)
	if err := runIn(opts.SrcDir, "go", compileArgs...); err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	// Step 5: pack C/C++ objects.
	packArgs := append([]string{"tool", "pack", "r", opts.Output}, compiledOFiles...)
	if err := runIn(opts.SrcDir, "go", packArgs...); err != nil {
		return fmt.Errorf("pack: %w", err)
	}

	// Pack _cgo_flags into archive so LDFLAGS propagate to the final link step.
	if _, err := os.Stat(cgoFlagsFile); err == nil {
		if err := runIn(opts.SrcDir, "go", "tool", "pack", "r", opts.Output, cgoFlagsFile); err != nil {
			return fmt.Errorf("pack _cgo_flags: %w", err)
		}
	}

	return nil
}

// --- helpers ---

// filterCppFlags removes -lc++ and -lstdc++ from flags when no C++ sources
// are present, avoiding unnecessary C++ standard library dependencies.
func filterCppFlags(flags []string) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		if f != "-lc++" && f != "-lstdc++" {
			out = append(out, f)
		}
	}
	return out
}

func runIn(dir, name string, args ...string) error {
	slog.Debug("exec", "cmd", name, "args", args, "dir", dir)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func run(name string, args ...string) error {
	slog.Debug("exec", "cmd", name, "args", args)
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func goRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func goEnv() (goos, goarch string) {
	goos = os.Getenv("GOOS")
	if goos == "" {
		out, _ := exec.Command("go", "env", "GOOS").Output()
		goos = strings.TrimSpace(string(out))
	}
	goarch = os.Getenv("GOARCH")
	if goarch == "" {
		out, _ := exec.Command("go", "env", "GOARCH").Output()
		goarch = strings.TrimSpace(string(out))
	}
	return
}

func extractPackageName(goFile string) string {
	data, err := os.ReadFile(goFile)
	if err != nil {
		return "main"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if after, ok := strings.CutPrefix(line, "package "); ok {
			return strings.TrimSpace(after)
		}
	}
	return "main"
}

// resolvePkgConfig scans Go cgo source files for #cgo pkg-config: directives,
// runs pkg-config to resolve them, and returns the resulting CFLAGS and LDFLAGS.
// This is necessary because go tool cgo does not process pkg-config directives;
// that processing is normally done by cmd/go (go build).
func resolvePkgConfig(srcDir string, cgoFiles []string) (cflags, ldflags []string, err error) {
	var pkgNames []string

	goos := os.Getenv("GOOS")
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := os.Getenv("GOARCH")
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	for _, f := range cgoFiles {
		path := filepath.Join(srcDir, f)
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		// Scan only the C preamble (lines before import "C").
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, `import "C"`) {
				break
			}

			// Match lines like: //#cgo pkg-config: foo bar
			// or: //#cgo linux pkg-config: foo bar
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") {
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "//")
			trimmed = strings.TrimSpace(trimmed)
			if !strings.HasPrefix(trimmed, "#cgo ") {
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "#cgo ")
			trimmed = strings.TrimSpace(trimmed)

			// Check for platform constraint before "pkg-config:".
			if idx := strings.Index(trimmed, "pkg-config:"); idx >= 0 {
				// Constraint is everything before "pkg-config:".
				constraint := strings.TrimSpace(trimmed[:idx])
				if constraint != "" && !matchesCgoConstraint(constraint, goos, goarch) {
					continue
				}
				pkgs := strings.TrimSpace(trimmed[idx+len("pkg-config:"):])
				if pkgs != "" {
					pkgNames = append(pkgNames, strings.Fields(pkgs)...)
				}
			}
		}
		file.Close()
	}

	if len(pkgNames) == 0 {
		return nil, nil, nil
	}

	slog.Debug("pkg-config", "packages", pkgNames)

	// Run pkg-config --cflags.
	cflagsOut, err := exec.Command("pkg-config", append([]string{"--cflags"}, pkgNames...)...).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("pkg-config --cflags %v: %w", pkgNames, err)
	}
	cflags = strings.Fields(strings.TrimSpace(string(cflagsOut)))

	// Run pkg-config --libs.
	ldflagsOut, err := exec.Command("pkg-config", append([]string{"--libs"}, pkgNames...)...).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("pkg-config --libs %v: %w", pkgNames, err)
	}
	ldflags = strings.Fields(strings.TrimSpace(string(ldflagsOut)))

	return cflags, ldflags, nil
}

// matchesCgoConstraint checks if a #cgo constraint (e.g., "linux", "!windows",
// "linux,amd64") matches the current build target.
func matchesCgoConstraint(constraint, goos, goarch string) bool {
	// Handle comma-separated AND constraints: "linux,amd64"
	parts := strings.Split(constraint, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		negate := false
		if strings.HasPrefix(part, "!") {
			negate = true
			part = part[1:]
		}
		match := part == goos || part == goarch
		if negate {
			match = !match
		}
		if !match {
			return false
		}
	}
	return true
}
