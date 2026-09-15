# Test: like thirdparty-local-replace, but the third-party package is a
# test-only dependency (testify/assert imported from greeter_test.go) and
# nothing local imports the replaced go-difflib: it is a test-only local
# package reachable only through testify/assert.
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
  pname = "thirdparty-local-replace-testonly";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
}
