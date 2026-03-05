// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
	cgoArgs := []string{
		"tool", "cgo",
		"-objdir", cgowork,
		"-importpath", opts.PFlag,
		"--",
		"-I", cgowork,
	}
	cgoArgs = append(cgoArgs, files.CgoFiles...)
	if err := runIn(opts.SrcDir, "go", cgoArgs...); err != nil {
		return fmt.Errorf("cgo: %w", err)
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
		if err := run(cc, "-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread", f, "-o", oFile); err != nil {
			return fmt.Errorf("cc %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	// Compile .S files with C preprocessor.
	for _, f := range files.SFiles {
		base := strings.TrimSuffix(f, ".S")
		oFile := filepath.Join(cgowork, base+"_asm_"+uid+".o")
		if err := run(cc, "-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread",
			filepath.Join(opts.SrcDir, f), "-o", oFile); err != nil {
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
		if err := run(cxx, "-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread",
			filepath.Join(opts.SrcDir, f), "-o", oFile); err != nil {
			return fmt.Errorf("cxx %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	// Step 3: test link + dynimport.
	cgoMainC := filepath.Join(cgowork, "_cgo_main.c")
	if _, err := os.Stat(cgoMainC); err == nil {
		mainO := filepath.Join(cgowork, "_cgo_main_"+uid+".o")
		_ = run(cc, "-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread", cgoMainC, "-o", mainO)

		testLinkO := filepath.Join(cgowork, "_cgo__"+uid+".o")
		linkArgs := append([]string{"-o", testLinkO, mainO}, compiledOFiles...)
		linkArgs = append(linkArgs, "-lpthread")
		if err := run(cc, linkArgs...); err != nil {
			slog.Debug("cgo test link failed (no dynamic imports)", "err", err)
		} else if _, err := os.Stat(testLinkO); err == nil {
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

	return nil
}

// --- helpers ---

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
