# Test: default mode with test-only third-party dep (testify/assert).
# test-drv-canary passes a stand-in for the CLI.
{
  pkgs ? import <nixpkgs> { },
  go2nix ? import ../../../packages/go2nix { inherit pkgs; },
}:
let
  inherit (pkgs) go;
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "testify-basic";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
  # greeter_test.go has a TestSkipMe that always fails; this proves
  # checkFlags reach the test binary.
  checkFlags = [ "-test.run=^TestGreet$" ];
}
