package compile

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/gofiles"
)

func compileCgo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	uid := strings.ReplaceAll(opts.ImportPath, "/", "_")

	// Signal that cgo was used (linker needs -extld).
	if nixBuildTop := os.Getenv("NIX_BUILD_TOP"); nixBuildTop != "" {
		if err := os.WriteFile(filepath.Join(nixBuildTop, ".has_cgo"), nil, 0o644); err != nil {
			return fmt.Errorf("writing .has_cgo: %w", err)
		}
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
	pkgCflags, pkgLdflags, err := resolvePkgConfig(opts.SrcDir, files.CgoFiles, opts.goos, opts.goarch)
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
		for line := range strings.SplitSeq(string(data), "\n") {
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
		if err := os.WriteFile(cgoFlagsFile, []byte("_CGO_LDFLAGS="+allFlags+"\n"), 0o644); err != nil {
			return fmt.Errorf("writing _cgo_flags: %w", err)
		}
	}

	// Step 2: compile C/C++ files.
	compiledOFiles, err := compileCFiles(cc, cxx, cgowork, opts.SrcDir, uid, files, cgoCflags, cgoCxxflags)
	if err != nil {
		return err
	}

	// Step 3: test link + dynimport.
	if err := cgoTestLinkAndDynimport(cc, cgowork, opts, uid, compiledOFiles, cgoFlagsLDFLAGS, cgoLdflags, cgoCflags); err != nil {
		return err
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

	// Step 4a: if .s files exist, generate symabis (go tool asm -gensymabis).
	// Go .s files are Plan 9 assembly and must always use go tool asm,
	// even in cgo packages. See cmd/go/internal/work/exec.go.
	hasSFiles := len(files.SFiles) > 0
	var symabisPath string
	asmhdr := filepath.Join(opts.TrimPath, "go_asm.h")
	if hasSFiles {
		if err := os.WriteFile(asmhdr, nil, 0o644); err != nil {
			return err
		}
		symabisPath = filepath.Join(opts.TrimPath, "symabis_"+uid)
		asmArgs := append(asmBaseArgs(opts), "-gensymabis", "-o", symabisPath)
		asmArgs = append(asmArgs, files.SFiles...)
		if err := runIn(opts.SrcDir, "go", asmArgs...); err != nil {
			return fmt.Errorf("gensymabis: %w", err)
		}
	}

	// Step 4b: compile Go + cgo-generated Go files.
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
	if hasSFiles {
		compileArgs = append(compileArgs, "-symabis", symabisPath, "-asmhdr", asmhdr)
	}
	compileArgs = append(compileArgs, extraGCFlags(opts)...)
	if embedFlag != "" {
		compileArgs = append(compileArgs, embedFlag)
	}
	compileArgs = append(compileArgs, files.GoFiles...)
	compileArgs = append(compileArgs, cgoGoFiles...)
	if err := runIn(opts.SrcDir, "go", compileArgs...); err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	// Step 4c: assemble .s files with go tool asm (using the real go_asm.h
	// generated by the compile step above).
	var asmOFiles []string
	if hasSFiles {
		for _, sf := range files.SFiles {
			base := strings.TrimSuffix(sf, ".s")
			objFile := filepath.Join(opts.TrimPath, base+"_"+uid+".o")
			asmFileArgs := append(asmBaseArgs(opts), "-o", objFile, sf)
			if err := runIn(opts.SrcDir, "go", asmFileArgs...); err != nil {
				return fmt.Errorf("asm %s: %w", sf, err)
			}
			asmOFiles = append(asmOFiles, objFile)
		}
	}

	// Step 5: pack C/C++ objects and assembly objects.
	allOFiles := append(compiledOFiles, asmOFiles...)
	packArgs := append([]string{"tool", "pack", "r", opts.Output}, allOFiles...)
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

// compileCFiles compiles C and C++ files, returning object file paths.
// Go .s files (Plan 9 assembly) are NOT handled here — they use go tool asm.
func compileCFiles(cc, cxx, cgowork, srcDir, uid string, files gofiles.PkgFiles, cgoCflags, cgoCxxflags []string) ([]string, error) {
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
		ccFiles = append(ccFiles, filepath.Join(srcDir, f))
	}

	for _, f := range ccFiles {
		base := strings.TrimSuffix(filepath.Base(f), ".c")
		oFile := filepath.Join(cgowork, base+"_"+uid+".o")
		ccArgs := []string{"-c", "-I", cgowork, "-I", srcDir, "-fPIC", "-pthread"}
		ccArgs = append(ccArgs, cgoCflags...)
		ccArgs = append(ccArgs, f, "-o", oFile)
		if err := runIn("", cc, ccArgs...); err != nil {
			return nil, fmt.Errorf("cc %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	// NOTE: Go .s files (Plan 9 assembly) are NOT compiled here.
	// They are always compiled with go tool asm, even in cgo packages.
	// Assembly handling is done in compileCgo() after the Go compile step.

	// Compile C++ files.
	for _, f := range files.CXXFiles {
		base := filepath.Base(f)
		base = strings.TrimSuffix(base, ".cc")
		base = strings.TrimSuffix(base, ".cpp")
		base = strings.TrimSuffix(base, ".cxx")
		oFile := filepath.Join(cgowork, base+"_cxx_"+uid+".o")
		cxxArgs := []string{"-c", "-I", cgowork, "-I", srcDir, "-fPIC", "-pthread"}
		cxxArgs = append(cxxArgs, cgoCxxflags...)
		cxxArgs = append(cxxArgs, filepath.Join(srcDir, f), "-o", oFile)
		if err := runIn("", cxx, cxxArgs...); err != nil {
			return nil, fmt.Errorf("cxx %s: %w", f, err)
		}
		compiledOFiles = append(compiledOFiles, oFile)
	}

	return compiledOFiles, nil
}

// cgoTestLinkAndDynimport performs the cgo test link and extracts dynamic imports.
func cgoTestLinkAndDynimport(cc, cgowork string, opts Options, uid string, compiledOFiles, cgoFlagsLDFLAGS, cgoLdflags, cgoCflags []string) error {
	cgoMainC := filepath.Join(cgowork, "_cgo_main.c")
	if _, err := os.Stat(cgoMainC); err != nil {
		return nil // no _cgo_main.c means cgo didn't generate one
	}

	mainO := filepath.Join(cgowork, "_cgo_main_"+uid+".o")
	mainArgs := []string{"-c", "-I", cgowork, "-I", opts.SrcDir, "-fPIC", "-pthread"}
	mainArgs = append(mainArgs, cgoCflags...)
	mainArgs = append(mainArgs, cgoMainC, "-o", mainO)
	if err := runIn("", cc, mainArgs...); err != nil {
		return fmt.Errorf("cc _cgo_main.c: %w", err)
	}

	testLinkO := filepath.Join(cgowork, "_cgo__"+uid+".o")
	linkArgs := append([]string{"-o", testLinkO, mainO}, compiledOFiles...)
	linkArgs = append(linkArgs, "-lpthread")
	linkArgs = append(linkArgs, cgoFlagsLDFLAGS...)
	linkArgs = append(linkArgs, cgoLdflags...)
	if err := runIn("", cc, linkArgs...); err != nil {
		// Test link may fail due to unresolved external symbols.
		// Retry allowing unresolved symbols since this binary is
		// only used to extract dynamic imports.
		var flag string
		switch opts.goos {
		case "darwin":
			flag = "-Wl,-undefined,dynamic_lookup"
		default:
			flag = "-Wl,--unresolved-symbols=ignore-all"
		}
		if err2 := runIn("", cc, append(linkArgs, flag)...); err2 != nil {
			slog.Debug("cgo test link failed (no dynamic imports)", "err", err)
			return nil // non-fatal: just means no dynamic imports
		}
	}
	if _, err := os.Stat(testLinkO); err == nil {
		pkgName := extractPackageName(filepath.Join(cgowork, "_cgo_gotypes.go"))
		dynOut := filepath.Join(cgowork, "_cgo_import_"+uid+".go")
		if err := runIn(opts.SrcDir, "go", "tool", "cgo",
			"-dynimport", testLinkO,
			"-dynout", dynOut,
			"-dynpackage", pkgName,
			"-dynlinker"); err != nil {
			return fmt.Errorf("cgo dynimport: %w", err)
		}
	}
	return nil
}

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

// resolvePkgConfig scans Go cgo source files for #cgo pkg-config: directives,
// runs pkg-config to resolve them, and returns the resulting CFLAGS and LDFLAGS.
// This is necessary because go tool cgo does not process pkg-config directives;
// that processing is normally done by cmd/go (go build).
func resolvePkgConfig(srcDir string, cgoFiles []string, goos, goarch string) (cflags, ldflags []string, err error) {
	var pkgNames []string

	for _, f := range cgoFiles {
		preamble := cgoPreamble(filepath.Join(srcDir, f))
		for _, line := range strings.Split(preamble, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "#cgo ") {
				continue
			}
			line = strings.TrimPrefix(line, "#cgo ")
			line = strings.TrimSpace(line)

			// Check for platform constraint before "pkg-config:".
			if idx := strings.Index(line, "pkg-config:"); idx >= 0 {
				constraint := strings.TrimSpace(line[:idx])
				if constraint != "" && !matchesCgoConstraint(constraint, goos, goarch) {
					continue
				}
				pkgs := strings.TrimSpace(line[idx+len("pkg-config:"):])
				if pkgs != "" {
					pkgNames = append(pkgNames, strings.Fields(pkgs)...)
				}
			}
		}
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

// cgoPreamble returns the cgo preamble text (the doc comment on import "C")
// from a Go source file. Returns "" if the file can't be parsed or has no
// import "C". Uses go/parser, matching cmd/go's approach.
func cgoPreamble(path string) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return ""
	}
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.GenDecl)
		if !ok || d.Tok != token.IMPORT {
			continue
		}
		for _, spec := range d.Specs {
			s, ok := spec.(*ast.ImportSpec)
			if !ok || s.Path.Value != `"C"` {
				continue
			}
			if s.Doc != nil {
				return s.Doc.Text()
			}
			if d.Doc != nil {
				return d.Doc.Text()
			}
		}
	}
	return ""
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
