// Package stamp is a non-cgo package with a //go:embed target so
// packageOverrides.srcOverlay can be exercised on the rawGoCompile path
// (cgo packages take the stdenv hook path instead).
package stamp

import _ "embed"

//go:embed VERSION
var Version string
