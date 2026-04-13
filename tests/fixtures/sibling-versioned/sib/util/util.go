// Package util exercises sibling-module identity: built with the sibling's
// own go directive (1.22, not the main module's 1.23) and recorded in
// modinfo as `dep example.com/sib v0.1.0` / `=> ./sib (devel)`.
package util

import "runtime"

// SourcePath returns the -trimpath-rewritten source path of this file as
// recorded in pclntab. cmd/go rewrites non-main-module source dirs to
// "<ModulePath>@<ModuleVersion>/…" (gc.go:271-276), so this should be
// "example.com/sib@v0.1.0/util/util.go".
func SourcePath() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}
