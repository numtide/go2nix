// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"fmt"
	"path/filepath"

	"github.com/numtide/go2nix/pkg/gofiles"
)

func compileGo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	args := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
		"-buildid", "", // deterministic empty buildID for Nix reproducibility
		"-trimpath=" + opts.TrimPath,
		"-pack",
		"-o", opts.Output,
	}
	if opts.GoVersion != "" {
		args = append(args, "-lang=go"+opts.GoVersion)
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
