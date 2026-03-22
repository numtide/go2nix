# Test: DAG mode with test-only local package (testutil helper).
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplicationDAGMode {
  pname = "test-helper-pkg";
  version = "0.0.1";
  src = ./. ;
  goLock = ./go2nix.toml;
  doCheck = true;
}
