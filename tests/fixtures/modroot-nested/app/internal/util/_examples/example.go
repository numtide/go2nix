// Package example is a module of its own below a local package, in a
// directory go list skips by name. cmd/go still treats it as another
// module (it will not embed across the go.mod), so it must not enter the
// util package's source.
package example
