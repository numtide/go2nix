# go2nix/nix/scope.nix — base scope for Go toolchain.
#
# Creates a self-referential package set via lib.makeScope.
# Provides: go, go2nix, stdlib, hooks, fetchers, helpers, buildGoApplication.
#
# Three builder approaches:
#   - gomod2nix: vendor + go build (simple, works everywhere)
#   - lockfile:  eval-time per-package DAG (fine-grained caching)
#   - dynamic:   recursive-nix + CA derivations (best CI performance)
{
  go,
  go2nix,
  lib,
  newScope,
  tags ? [ ],
  netrcFile ? null,
  nixPackage ? null,
}:
let
  tagFlag = if tags == [ ] then "" else builtins.concatStringsSep "," tags;

  # Feature detection: dynamic derivations require builtins.outputOf
  # (available when Nix has the dynamic-derivations experimental feature).
  hasDynamicDerivations = builtins ? outputOf;
in
lib.makeScope newScope (
  self:
  let
    inherit (self) callPackage;

    buildGoApplicationLockfile = callPackage ./lockfile { };

    buildGoApplicationGomod2nix = callPackage ./gomod2nix { };

    buildGoApplicationDynamic' =
      if nixPackage != null then
        callPackage ./dynamic { inherit nixPackage; }
      else
        throw "buildGoApplicationDynamic requires nixPackage to be set in mk-go-env";
  in
  {
    inherit
      go
      go2nix
      lib
      tags
      tagFlag
      netrcFile
      nixPackage
      hasDynamicDerivations
      ;

    helpers = import ./helpers.nix;

    stdlib = callPackage ./stdlib.nix { };

    hooks = callPackage ./lockfile/hooks { };

    fetchers = {
      fetchGoModule = callPackage ./lockfile/fetch-go-module.nix { };
    };

    # When nixPackage is provided and Nix supports dynamic derivations,
    # automatically use the dynamic path. Otherwise fall back to the
    # lockfile-based builder.
    buildGoApplication =
      if hasDynamicDerivations && nixPackage != null then
        buildGoApplicationDynamic'
      else
        buildGoApplicationLockfile;

    # Explicit access to each builder path.
    inherit buildGoApplicationLockfile buildGoApplicationGomod2nix;
    buildGoApplicationDynamic = buildGoApplicationDynamic';
  }
)
