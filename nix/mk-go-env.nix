# go2nix/nix/mk-go-env.nix — creates a reusable Go toolchain scope.
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
  netrcFile ? null,
  nixPackage ? null,
  # Env vars forwarded to stdlib compilation and go tool invocations.
  # Scope-level because stdlib is scope-level — every buildGoApplication
  # call in this scope shares the same stdlib derivation.
  goEnv ? { },
}:
callPackage ./scope.nix {
  inherit
    go
    go2nix
    tags
    netrcFile
    nixPackage
    goEnv
    ;
}
