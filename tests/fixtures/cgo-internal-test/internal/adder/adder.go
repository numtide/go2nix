// Package adder exposes a cgo-backed addition function. It exists to
// exercise the testrunner's internal-test compile path for cgo packages.
package adder

/*
static int c_add(int a, int b) { return a + b; }
*/
import "C"

// Add returns a+b via C.
func Add(a, b int) int {
	return int(C.c_add(C.int(a), C.int(b)))
}
