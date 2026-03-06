# go2nix/lib.nix — public API for use outside the flake.
#
# Usage:
#   mkGoEnv { go, go2nix, callPackage, tags?, netrcFile? }
#     Returns a scope with buildGoApplication, stdlib, hooks, fetchers.
_: {
  mkGoEnv = args: import ./nix/mk-go-env.nix args;
}
