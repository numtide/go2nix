# go2nix/nix/scope.nix — base scope for Go toolchain.
#
# Creates a self-referential package set via lib.makeScope.
# Provides: go, go2nix, stdlib, hooks, fetchers, helpers, buildGoApplication.
#
# Three builder modes:
#   - vendor:  vendor + go build (simple, works everywhere)
#   - DAG:     eval-time per-package DAG (fine-grained caching)
#   - dynamic: recursive-nix + CA derivations (best CI performance)
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

    buildGoApplicationDAGMode = callPackage ./dag { };

    buildGoApplicationVendorMode = callPackage ./vendor { };

    buildGoApplicationDynamicMode' =
      if nixPackage != null then
        callPackage ./dynamic { inherit nixPackage; }
      else
        throw "buildGoApplicationDynamicMode requires nixPackage to be set in mk-go-env";
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

    hooks = callPackage ./dag/hooks { };

    fetchers = {
      fetchGoModule = callPackage ./dag/fetch-go-module.nix { };
    };

    # When nixPackage is provided and Nix supports dynamic derivations,
    # automatically use the dynamic path. Otherwise fall back to the
    # DAG-based builder.
    buildGoApplication =
      if hasDynamicDerivations && nixPackage != null then
        buildGoApplicationDynamicMode'
      else
        buildGoApplicationDAGMode;

    # Explicit access to each builder mode.
    inherit buildGoApplicationDAGMode buildGoApplicationVendorMode;
    buildGoApplicationDynamicMode = buildGoApplicationDynamicMode';
  }
)
