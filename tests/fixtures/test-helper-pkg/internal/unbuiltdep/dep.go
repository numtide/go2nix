// Package unbuiltdep is only imported from cmd/unbuilt, which is NOT
// in the build's subPackages closure. It exercises the testrunner's
// scope filter: without it, the testrunner would try to compile
// cmd/unbuilt's test against an importcfg that has no entry for this
// package and fail with "could not import …".
package unbuiltdep

func Answer() int { return 42 }
