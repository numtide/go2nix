// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"github.com/numtide/go2nix/pkg/gofiles"
)

func compileGo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	args := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
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

	return runIn(opts.SrcDir, "go", args...)
}
