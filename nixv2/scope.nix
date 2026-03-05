# go2nix/nix2/scope.nix — base scope for Go package set.
#
# Creates a self-referential package set via lib.makeScope.
# Provides: go, go2nix, stdlib, hooks, fetchers, helpers, buildGoApplication.
# The `require` list starts empty and is populated by mk-go-env.nix's overlay.
{ go, go2nix, lib, newScope, tags ? [] }:
let
  tagFlag = if tags == [] then "" else builtins.concatStringsSep "," tags;
in
lib.makeScope newScope (self:
  let inherit (self) callPackage; in
  {
    inherit go go2nix lib tags tagFlag;

    helpers = import ./helpers.nix;
    parseGoMod = import ./go-mod-parser.nix;

    stdlib = callPackage ./stdlib.nix {};

    hooks = callPackage ./hooks {};

    fetchers = {
      fetchGoModule = callPackage ./fetch-module.nix {};
    };

    # Populated by mk-go-env.nix overlay.
    require = [];

    buildGoApplication = callPackage ./build-go-application.nix {};
  }
)
