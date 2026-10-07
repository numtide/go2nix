// Package a has an internal test and an external test that imports
// internal/b, which imports a: testing a recompiles b from its directory in
// the main module's tree, below go.mod, which is the one place the test
// runner starts `go` inside the module.
package a

import "example.com/dep"

func Greeting() string { return dep.Greeting() }
