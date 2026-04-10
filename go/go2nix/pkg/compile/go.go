// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"fmt"
	"path/filepath"

	"github.com/numtide/go2nix/pkg/gofiles"
)

func compileGo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	args := append(baseCompileArgs(opts), opts.outputFlags()...)
	if opts.GoVersion != "" {
		args = append(args, "-lang=go"+opts.GoVersion)
	}
	// Pass -complete when the package has only Go source files (no C, C++,
	// Fortran, or syso files), matching cmd/go behavior (gc.go:86-103).
	// CgoFiles and SFiles are already zero here (routing in CompileGoPackage).
	if len(files.CFiles) == 0 && len(files.CXXFiles) == 0 && len(files.FFiles) == 0 && len(files.SysoFiles) == 0 {
		args = append(args, "-complete")
	}
	if opts.concurrency > 1 {
		args = append(args, fmt.Sprintf("-c=%d", opts.concurrency))
	}
	if opts.pgoPreprofile != "" {
		args = append(args, "-pgoprofile="+opts.pgoPreprofile)
	}
	args = append(args, extraGCFlags(opts)...)
	if embedFlag != "" {
		args = append(args, embedFlag)
	}
	args = append(args, files.GoFiles...)

	if err := runIn(opts.SrcDir, "go", args...); err != nil {
		return err
	}

	// Pack .syso (pre-compiled system object) files into the archive,
	// matching cmd/go behavior (exec.go).
	if len(files.SysoFiles) > 0 {
		var sysoAbsPaths []string
		for _, s := range files.SysoFiles {
			sysoAbsPaths = append(sysoAbsPaths, filepath.Join(opts.SrcDir, s))
		}
		packArgs := append([]string{"tool", "pack", "r", opts.Output}, sysoAbsPaths...)
		if err := runIn(opts.SrcDir, "go", packArgs...); err != nil {
			return fmt.Errorf("pack syso: %w", err)
		}
	}

	return nil
}
