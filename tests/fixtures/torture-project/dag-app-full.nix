# Test: default mode with modRoot + local replace directives.
#
# app-full sits inside a monorepo and uses replace directives to reference
# 14 sibling modules under ../internal/*. This exercises the hard case where
# src is the monorepo root, modRoot points to one app, and local replace
# targets live outside modRoot.
let
  pkgs = import <nixpkgs> { };
  go = pkgs.go_1_26;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "torture-app-full";
  version = "0.0.1";
  src = ./.;
  goLock = ./app-full/go2nix.toml;
  modRoot = "app-full";
  subPackages = [ "cmd/app-full" ];
}
