// Package testutil is a test-only helper imported solely from sib's
// *_test.go files. It is not in any subPackage's build closure.
//
// Crucially, testutil itself imports sib — so when `go list -test`
// expands sib's tests, testutil only appears as the recompiled
// variant `sib/testutil [sib.test]`, never in non-variant form.
package testutil

import "example.com/sib"

func MustGreet(who string) string {
	got := sib.Greet(who)
	if got == "" {
		panic("empty")
	}
	return got
}
