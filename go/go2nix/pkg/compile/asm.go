package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/gofiles"
)

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
	compileArgs = append(compileArgs, extraGCFlags(opts)...)
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
