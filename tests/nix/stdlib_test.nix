# tests/nix/stdlib_test.nix — unit tests for nix/stdlib.nix
#
# Returns true on success, throws on failure. Exercised by the
# `nix-unit-tests` flake check; run standalone with
#   nix eval -f tests/nix/stdlib_test.nix --arg pkgs 'import <nixpkgs> {}'
{ pkgs }:
let
  inherit (pkgs) lib;
  stdlib = pkgs.callPackage ../../nix/stdlib.nix { };
  tagged = stdlib.override {
    tags = [
      "osusergo"
      "netgo"
    ];
  };
  taggedCgoOff = tagged.override { goEnv.CGO_ENABLED = "0"; };
  mkScope =
    args:
    import ../../nix/mk-go-env.nix (
      {
        inherit (pkgs) go callPackage;
        go2nix = pkgs.emptyFile;
      }
      // args
    );
in
# tags = [ ] must leave the derivation alone: every build that sets no tags
# depends on its path.
assert
  (stdlib.override { tags = [ ]; }).drvPath == stdlib.drvPath
  || throw "tags = [ ] must not change the stdlib derivation";
assert
  lib.hasInfix " go install -v --trimpath std " stdlib.buildCommand
  || throw "default stdlib must run `go install -v --trimpath std`";
assert stdlib.name == "go-stdlib-${pkgs.go.version}" || throw "default stdlib name changed";
# cmd/go reads -tags as a set, so order and repeats must not make another
# derivation.
assert
  lib.hasInfix " go install -v -tags netgo,osusergo --trimpath std " tagged.buildCommand
  || throw "tags must reach `go install std`, sorted";
assert
  tagged.drvPath == (stdlib.override {
    tags = [
      "netgo"
      "osusergo"
      "netgo"
    ];
  }).drvPath
  || throw "the same set of tags must give the same stdlib derivation";
assert tagged.drvPath != stdlib.drvPath || throw "tags must give another stdlib derivation";
assert lib.hasInfix "-tags-" tagged.name || throw "a tagged stdlib must say so in its name";
assert
  lib.hasInfix "export CGO_ENABLED=0" taggedCgoOff.buildCommand
  && lib.hasInfix " -tags netgo,osusergo " taggedCgoOff.buildCommand
  || throw "goEnv and tags must both reach the build";
# mkGoEnv's own `tags` is stored on the scope and read by neither builder;
# the scope's callPackage must not hand it to stdlib.nix.
assert
  (mkScope { tags = [ "netgo" ]; }).stdlib.drvPath == (mkScope { }).stdlib.drvPath
  || throw "mkGoEnv's tags must not change the scope's stdlib";
true
