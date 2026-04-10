# Regression: GOFIPS140=latest must be honoured end-to-end. The scope-level
# goEnv reaches both `go install std` (FIPS-aware crypto/internal/fips140)
# and the link drv, where link-binary emits build GOFIPS140=latest,
# DefaultGODEBUG=...,fips140=on,... and passes -fipso to go tool link.
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
    goEnv = {
      GOFIPS140 = "latest";
    };
  };
in
goEnv.buildGoApplication {
  pname = "fips140-latest";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = false;
}
