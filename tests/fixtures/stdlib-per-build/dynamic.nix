# Test: the builds of dag.nix through buildGoApplicationExperimental.
#
# Requires: recursive-nix, ca-derivations, dynamic-derivations experimental features.
{
  variant ? "default",
}:
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  v = (import ./variants.nix).${variant};
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
    nixPackage = pkgs.nixVersions.nix_2_34;
    goEnv = v.scopeGoEnv or { };
  };
in
goEnv.buildGoApplicationExperimental (
  {
    pname = "stdlib-per-build";
    src = ./.;
    goLock = ./go2nix.toml;
  }
  // (v.build or { })
)
