# go2nix/nix/scope.nix — base scope for Go toolchain.
#
# Creates a self-referential package set via lib.makeScope.
# Provides: go, go2nix, stdlib, hooks, fetchers, helpers, buildGoApplication.
#
# Two builder modes:
#   - default:      eval-time per-package DAG (fine-grained caching)
#   - experimental: recursive-nix + CA derivations (requires experimental nix features)
{
  go,
  go2nix,
  lib,
  stdenv,
  newScope,
  tags ? [ ],
  netrcFile ? null,
  nixPackage ? null,
  goEnv ? { },
}:
let
  tagFlag = if tags == [ ] then "" else builtins.concatStringsSep "," tags;

  # Feature detection: dynamic derivations require builtins.outputOf
  # (available when Nix has the dynamic-derivations experimental feature).
  hasDynamicDerivations = builtins ? outputOf;

  # Cross-compilation: Go selects the target via GOOS/GOARCH env, not via
  # drv `system`. stdlib (`go install std`) and per-package compiles
  # (`go tool compile`) both read these from goEnv, so default them from
  # the Nix host platform when cross-compiling. User-supplied goEnv wins.
  # Native builds keep goEnv untouched so derivation hashes don't change.
  crossGoEnv = lib.optionalAttrs (stdenv.buildPlatform != stdenv.hostPlatform) {
    inherit (stdenv.hostPlatform.go) GOOS GOARCH;
  };
  goEnv' = crossGoEnv // goEnv;
in
lib.makeScope newScope (
  self:
  let
    inherit (self) callPackage;

    buildGoApplication = callPackage ./dag { };

    buildGoApplicationExperimental' =
      if nixPackage != null then
        callPackage ./dynamic { inherit nixPackage; }
      else
        throw "buildGoApplicationExperimental requires nixPackage to be set in mk-go-env";
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
    goEnv = goEnv';

    helpers = import ./helpers.nix;

    stdlib = callPackage ./stdlib.nix { goEnv = goEnv'; };

    hooks = callPackage ./dag/hooks { };

    fetchers = {
      fetchGoModule = callPackage ./dag/fetch-go-module.nix { };
    };

    # Default builder: eval-time per-package DAG.
    inherit buildGoApplication;

    # Experimental builder: recursive-nix + CA derivations.
    buildGoApplicationExperimental = buildGoApplicationExperimental';
  }
)
