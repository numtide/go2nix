# Test: default mode with a test-only local package (internal/testutil
# is only imported from internal/app/app_test.go). doCheck exercises the
# plugin's testLocalPackages output and the dag builder's union of those
# into localPackages so the testrunner has an .a archive for the helper.
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
  pname = "test-helper-pkg";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
}
