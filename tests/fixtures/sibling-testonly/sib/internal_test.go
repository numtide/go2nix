package sib

import "testing"

// An internal-package test file (package sib, not sib_test) means
// `go list -test` emits a recompiled `sib [sib.test]` variant. Since
// testutil imports sib, testutil is then only emitted in variant form
// `sib/testutil [sib.test]` — never the bare path. This is the trigger
// for the parse_test_packages skip-too-early bug.
func TestGreetInternal(t *testing.T) {
	if Greet("x") != "hello x" {
		t.Fatal("unexpected")
	}
}
