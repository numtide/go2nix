// Package ignored is a nested module that go list stops at. Regression
// fixture for the pkgSrc/mainSrc filters: this file must not enter either
// store path.
package ignored

const DoesNotCompile = nonexistent
