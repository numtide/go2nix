# Test: default mode with custom build tags.
#
# internal/feature has //go:build mytag and //go:build !mytag twins.
# tags = ["mytag"] must reach the eval-time `go list` (resolveGoPackages),
# the per-package compile manifest, and modinfo (`build -tags=mytag`).
# The wrapper asserts the binary prints "on" — "off" would mean tags
# were dropped somewhere in the chain.
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
  pname = "build-tags";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  tags = [ "mytag" ];
}
