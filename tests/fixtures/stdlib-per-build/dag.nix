# Test: a per-build CGO_ENABLED and per-build tags reach the standard
# library, every package compile, the link and the tests.
#
# main.go reaches net, os/user and time, whose files depend on CGO_ENABLED
# and on the netgo, osusergo and timetzdata tags; internal/dualcgo has a
# `cgo` and a `!cgo` twin. variants.nix lists the builds.
{
  variant ? "default",
  doCheck ? true,
}:
let
  pkgs = import <nixpkgs> { };
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  v = (import ./variants.nix).${variant};
  # The same toolchain under another `version`: nothing else of it is read
  # to tell whether it is the release the plugin runs.
  go = pkgs.go // {
    version = pkgs.go.version + (v.goVersionSuffix or "");
  };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
    goEnv = v.scopeGoEnv or { };
  };
in
goEnv.buildGoApplication (
  {
    pname = "stdlib-per-build";
    version = "0.0.1";
    src = ./.;
    goLock = ./go2nix.toml;
    inherit doCheck;
    inherit (v) env;
  }
  // (v.build or { })
)
