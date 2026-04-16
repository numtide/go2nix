// Package unreachable is in the sibling module but is not imported by
// anything in sibling-testonly's build closure or its tests. It exists to
// regression-test that parse_test_packages drops packages surfaced by the
// `<sibling>/...` pattern that aren't reachable from a build-closure
// local's *_test.go files: its third-party import is not in the building
// module's go.sum, so a compile drv for it would fail with
// "could not import rsc.io/quote (open : no such file or directory)".
package unreachable

import "rsc.io/quote"

func Hello() string { return quote.Hello() }
