# go2nix/nixv2/scope.nix — base scope for Go toolchain.
#
# Creates a self-referential package set via lib.makeScope.
# Provides: go, go2nix, stdlib, hooks, fetchers, helpers, buildGoApplication.
{ go, go2nix, lib, newScope, tags ? [] }:
let
  tagFlag = if tags == [] then "" else builtins.concatStringsSep "," tags;
in
lib.makeScope newScope (self:
  let inherit (self) callPackage; in
  {
    inherit go go2nix lib tags tagFlag;

    helpers = import ./helpers.nix;

    stdlib = callPackage ./stdlib.nix {};

    hooks = callPackage ./hooks {};

    fetchers = {
      fetchGoModule = callPackage ./fetch-go-module.nix {};
    };

    buildGoApplication = callPackage ./build-go-application.nix {};
  }
)
