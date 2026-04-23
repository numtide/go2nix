# Regression test: modRoot with a ./ prefix and the main package at the
# module root. The -trimpath rewrite key must match the canonical srcdir
# the compiler records; if the ./ survives into moduleRoot, the rewrite
# misses and the binary references mainSrc — caught by disallowedReferences.
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
  pname = "modroot-dotslash";
  version = "0.0.1";
  src = ./.;
  modRoot = "./app";
  doCheck = false;
}
