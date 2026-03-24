# Shared: pre-fetch torture-project Go module download cache.
# Reuses nixpkgs buildGoModule's goModules FOD (proxyVendor mode) which
# handles non-deterministic metadata normalization.
#
# The output is the download cache (zips + mod files), not full GOMODCACHE.
# Tests that need extracted source trees should set GOPROXY=file://${this}
# and run `go mod download` into a writable GOMODCACHE.
{ pkgs, testFixtures }:

(pkgs.buildGoModule {
  pname = "torture-project-modules";
  version = "0-unstable";
  src = testFixtures + "/torture-project";
  vendorHash = "sha256-DJGjAcVVOQT2L8hINdZgOaJ68RsGKsgZD9UJQdOCqDc=";
  proxyVendor = true;
}).goModules
