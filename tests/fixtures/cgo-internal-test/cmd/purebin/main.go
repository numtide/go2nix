// purebin is a pure-Go binary built in the same link-binary invocation as
// the cgo root binary. Regression for the .has_cgo marker leaking across
// SubPackages iterations: this must NOT be linked with -linkmode external.
package main

import "fmt"

func main() {
	fmt.Println("pure")
}
