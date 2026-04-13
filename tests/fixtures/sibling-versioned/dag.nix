# Regression fixture for sibling-module identity (cmd/go parity):
#   - sib/ is `replace example.com/sib => ./sib` with `require ... v0.1.0`
#   - sib/go.mod has `go 1.22` (≠ main's 1.23) so per-package -lang differs
#   - the binary is asserted by tests/nix/sibling_versioned_test.nix:
#       * `go version -m` contains the `dep`/`=>` lines for the sibling
#       * runtime.Caller in sib/util reports `example.com/sib@v0.1.0/...`
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
  pname = "sibling-versioned";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  subPackages = [ "./cmd/app" ];
}
