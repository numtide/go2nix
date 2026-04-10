# Test: default mode, lockfile-free, with a fork-style replace directive.
#
# go.mod has `replace go.uber.org/atomic => github.com/uber-go/atomic v1.11.0`,
# so go.sum lists only the replacement path. Regression for moduleHashes
# re-keying: the plugin must return hashes keyed by "go.uber.org/atomic@v1.11.0"
# (the modKey nix/dag looks up), not the replacement path.
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
  pname = "torture-app-replace";
  version = "0.0.1";
  src = ./.;
  modRoot = "app-replace";
  subPackages = [ "cmd/app-replace" ];
}
