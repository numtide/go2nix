// Package testutil is imported only from *_test.go and so is a
// test-only-local. It is never a build-graph package.
package testutil

func Expected() string {
	return "hello world"
}
