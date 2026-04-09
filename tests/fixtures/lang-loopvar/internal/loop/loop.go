// Package loop exercises for-loop variable capture semantics.
//
// With go.mod declaring `go 1.21`, this package must be compiled with
// -lang=go1.21 so the loop variable is shared across iterations (pre-1.22
// semantics). go2nix's per-package source filter for non-root local
// packages excludes go.mod, so the build-time go.mod walk cannot recover
// the language version — it must be threaded explicitly from eval-time
// `go list` output. Without that, the toolchain default (>=1.22) flips to
// per-iteration capture and Capture() returns [0 1 2] instead of [3 3 3].
package loop

func Capture() []int {
	var fns []func() int
	for i := 0; i < 3; i++ {
		fns = append(fns, func() int { return i })
	}
	out := make([]int, 0, len(fns))
	for _, f := range fns {
		out = append(out, f())
	}
	return out
}
