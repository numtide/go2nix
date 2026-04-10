# Test: default mode checkPhase for a local cgo package with internal _test.go
# files. Regression for the testrunner dropping CgoFiles/SFiles/SysoFiles when
# building the internal test archive.
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
  pname = "cgo-internal-test";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
}
