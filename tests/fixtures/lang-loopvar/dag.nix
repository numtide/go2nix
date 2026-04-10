# Regression: -lang must be threaded to non-root local packages.
# go.mod says `go 1.21`; internal/loop relies on pre-1.22 shared-loopvar
# semantics. The per-package src filter excludes go.mod, so without an
# explicit --go-version the subpackage would compile under the toolchain's
# default language version and the binary would print [0 1 2].
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
  pname = "lang-loopvar";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
}
