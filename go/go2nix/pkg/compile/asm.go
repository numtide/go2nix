package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/gofiles"
)

// asmBaseArgs returns the common flags for go tool asm, matching
// cmd/go/internal/work.asmArgs.
func asmBaseArgs(opts Options) []string {
	args := []string{
		"tool", "asm",
		"-p", opts.PFlag,
		"-trimpath", opts.trimRewrite,
		"-I", opts.TrimPath,
		"-I", filepath.Join(opts.goroot, "pkg", "include"),
		"-D", "GOOS_" + opts.goos, "-D", "GOARCH_" + opts.goarch,
	}
	args = append(args, opts.asmArchDefs...)
	return args
}

// generateSymabis creates an empty go_asm.h and runs go tool asm -gensymabis
// for the given assembly files. Returns the paths to go_asm.h and the symabis file.
func generateSymabis(opts Options, uid string, sfiles []string) (asmhdr, symabis string, err error) {
	// go_asm.h must be named exactly "go_asm.h" — assembly files #include it.
	asmhdr = filepath.Join(opts.TrimPath, "go_asm.h")
	if err := os.WriteFile(asmhdr, nil, 0o644); err != nil {
		return "", "", err
	}
	symabis = filepath.Join(opts.TrimPath, "symabis_"+uid)
	asmArgs := append(asmBaseArgs(opts), "-gensymabis", "-o", symabis)
	asmArgs = append(asmArgs, sfiles...)
	if err := runIn(opts.SrcDir, "go", asmArgs...); err != nil {
		return "", "", fmt.Errorf("gensymabis: %w", err)
	}
	return asmhdr, symabis, nil
}

func compileWithAsm(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	uid := strings.ReplaceAll(opts.ImportPath, "/", "_")

	// Pass 1: generate symabis.
	asmhdr, symabis, err := generateSymabis(opts, uid, files.SFiles)
	if err != nil {
		return err
	}

	// Pass 2: compile Go with symabis + asmhdr.
	compileArgs := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
		"-buildid", "", // deterministic empty buildID for Nix reproducibility
		"-trimpath=" + opts.trimRewrite,
		"-symabis", symabis,
		"-asmhdr", asmhdr,
		"-pack",
		"-o", opts.Output,
	}
	if opts.GoVersion != "" {
		compileArgs = append(compileArgs, "-lang=go"+opts.GoVersion)
	}
	if opts.concurrency > 1 {
		compileArgs = append(compileArgs, fmt.Sprintf("-c=%d", opts.concurrency))
	}
	if opts.pgoPreprofile != "" {
		compileArgs = append(compileArgs, "-pgoprofile="+opts.pgoPreprofile)
	}
	compileArgs = append(compileArgs, extraGCFlags(opts)...)
	if embedFlag != "" {
		compileArgs = append(compileArgs, embedFlag)
	}
	compileArgs = append(compileArgs, files.GoFiles...)
	if err := runIn(opts.SrcDir, "go", compileArgs...); err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	// Pass 3: assemble each .s file.
	var ofiles []string
	for _, sf := range files.SFiles {
		base := strings.TrimSuffix(sf, ".s")
		objFile := filepath.Join(opts.TrimPath, base+"_"+uid+".o")
		asmFileArgs := append(asmBaseArgs(opts), "-o", objFile, sf)
		if err := runIn(opts.SrcDir, "go", asmFileArgs...); err != nil {
			return fmt.Errorf("asm %s: %w", sf, err)
		}
		ofiles = append(ofiles, objFile)
	}

	// Pack .syso (pre-compiled system object) files alongside assembly objects.
	for _, s := range files.SysoFiles {
		ofiles = append(ofiles, filepath.Join(opts.SrcDir, s))
	}

	// Pack all object files in a single call.
	if len(ofiles) > 0 {
		packArgs := append([]string{"tool", "pack", "r", opts.Output}, ofiles...)
		if err := runIn(opts.SrcDir, "go", packArgs...); err != nil {
			return fmt.Errorf("pack: %w", err)
		}
	}

	return nil
}
