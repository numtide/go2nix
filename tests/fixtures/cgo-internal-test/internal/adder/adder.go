// Package adder exposes a cgo-backed addition function. It exists to
// exercise the testrunner's internal-test compile path for cgo packages,
// and (via the //go:embed below plus a second one in adder_test.go) the
// build-time embedcfg + testrunner MergeEmbedCfg paths.
package adder

import _ "embed"

/*
static int c_add(int a, int b) { return a + b; }
*/
import "C"

//go:embed data.txt
var Data string

// Banner returns the embedded data.txt contents.
func Banner() string { return Data }

// Add returns a+b via C.
func Add(a, b int) int {
	return int(C.c_add(C.int(a), C.int(b)))
}
