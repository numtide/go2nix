// Package brokenembed has a //go:embed pattern with no on-disk match.
// It is not imported by any subPackage and has no tests, so the testrunner
// must skip it (counted in skipped) without trying to resolve the pattern.
// Regression: ListLocalPackages used to resolve patterns eagerly during
// the walk, failing the whole listing before the LocalArchives filter could
// drop this package.
package brokenembed

import "embed"

//go:embed missing.txt
var FS embed.FS
