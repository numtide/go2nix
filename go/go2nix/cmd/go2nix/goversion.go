package main

import (
	"strings"

	"github.com/numtide/go2nix/pkg/compile"
)

// resolveGoVersion returns the target Go toolchain major.minor version for
// build-tag evaluation. If flagValue is non-empty it wins (stripped of any
// leading "go" and patch component); otherwise it falls back to
// `go env GOVERSION` from the toolchain on PATH.
func resolveGoVersion(flagValue string) string {
	if flagValue != "" {
		return compile.LangVersion(strings.TrimPrefix(flagValue, "go"))
	}
	v := compile.GoEnvVar("GOVERSION")
	if v == "" {
		return ""
	}
	return compile.LangVersion(strings.TrimPrefix(v, "go"))
}
