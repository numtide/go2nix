# Test: dag mode with contentAddressed enabled.
# Exercises the CA early-cutoff path.
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
  pname = "xtest-local-dep";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
  contentAddressed = true;
}
