# Test: default mode with modRoot + local replace directives (partial app).
#
# app-partial sits inside a monorepo and uses replace directives to reference
# 5 sibling modules under ../internal/*. Fewer dependencies than app-full,
# used for cross-app isolation benchmarks.
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
  pname = "torture-app-partial";
  version = "0.0.1";
  src = ./.;
  goLock = ./app-partial/go2nix.toml;
  modRoot = "app-partial";
  subPackages = [ "cmd/app-partial" ];
}
