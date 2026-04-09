module github.com/numtide/go2nix/torture/app-replace

go 1.23

require go.uber.org/atomic v1.11.0

// Fork replace: original path != replacement path. go.sum lists the
// replacement path, while modKey is "go.uber.org/atomic@v1.11.0".
// Regression fixture for lockfile-free moduleHashes re-keying.
replace go.uber.org/atomic => github.com/uber-go/atomic v1.11.0
