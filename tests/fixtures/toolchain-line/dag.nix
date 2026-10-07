# Regression fixture for `toolchain` lines newer than the builder's Go:
#   - go.mod has `toolchain go1.99.0`
#   - dep/ is `replace example.com/dep => ./dep`, and dep/go.mod has the same line
#   - `go build` with GOTOOLCHAIN=local (what the plugin evaluates with) ignores
#     both; cmd/go ignores a dependency's line under any GOTOOLCHAIN
# The compiles of example.com/dep and of the root package and the compile of
# main in link-binary start `go` next to one of the two go.mod files. The test
# runner compiles the tests under $NIX_BUILD_TOP; it is inside the module when
# it recompiles a local package for an external test, which internal/a's
# b_test.go makes it do for internal/b. All of it with no network.
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "toolchain-line";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
}
