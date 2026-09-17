# go2nix/lib.nix — public API for use outside the flake.
#
# Usage:
#   mkGoEnv { go, go2nix, callPackage, tags?, netrcFile?, nixPackage?, goEnv? }
#     Returns a scope with buildGoApplication, buildGoApplicationExperimental,
#     stdlib, hooks, fetchers, helpers (see nix/scope.nix).
_: {
  mkGoEnv = args: import ./nix/mk-go-env.nix args;
}
