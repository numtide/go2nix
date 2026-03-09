# go2nix/nix/scope.nix — base scope for Go toolchain.
#
# Creates a self-referential package set via lib.makeScope.
# Provides: go, go2nix, stdlib, hooks, fetchers, helpers, buildGoApplication.
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

    buildGoApplicationLockfile = callPackage ./build-go-application.nix { };

    buildGoApplicationDynamic' =
      if nixPackage != null then
        callPackage ./build-go-application-dynamic.nix { inherit nixPackage; }
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

    hooks = callPackage ./hooks { };

    fetchers = {
      fetchGoModule = callPackage ./fetch-go-module.nix { };
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
    inherit buildGoApplicationLockfile;
    buildGoApplicationDynamic = buildGoApplicationDynamic';
  }
)
