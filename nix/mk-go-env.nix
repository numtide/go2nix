# go2nix/nixv2/mk-go-env.nix — creates a reusable Go toolchain scope.
#
# Returns a scope with:
#   goEnv.buildGoApplication { ... }   — build a Go binary (99% of use cases)
#   goEnv.go / go2nix / stdlib         — toolchain
#   goEnv.hooks                        — setup hooks for compilation
#   goEnv.fetchers                     — module fetchers
{
  go,
  go2nix,
  callPackage,
  tags ? [ ],
}:
callPackage ./scope.nix { inherit go go2nix tags; }
