# tests/nix/mainsrc_replace_test.nix — regression for mainSrc dropping
# sibling-replace dirs.
#
# When modRoot != "." and go.mod has `replace => ../sibling`, the link
# derivation's mainSrc must include those sibling module roots so the
# testrunner (which re-walks go.mod from ${mainSrc}/${modRoot}) can
# resolve them. torture-project/app-full has 14 such siblings under
# ../internal/*.
#
# Eval-only: mainSrc is computed from go.mod parsing alone and does not
# force goPackagesResult, so this check does not need the plugin.
{
  lib,
  pkgs,
  runCommand,
}:
let
  go2nix = import ../../packages/go2nix { inherit pkgs; };
  scope = import ../../nix/mk-go-env.nix {
    inherit (pkgs) go callPackage;
    inherit go2nix;
  };
  mkApp =
    doCheck:
    scope.buildGoApplication {
      pname = "mainsrc-replace-test";
      version = "0.0.1";
      src = ../fixtures/torture-project;
      goLock = ../fixtures/torture-project/app-full/go2nix.toml;
      modRoot = "app-full";
      subPackages = [ "cmd/app-full" ];
      inherit doCheck;
    };
  ms = (mkApp true).passthru.mainSrc;
  msNoCheck = (mkApp false).passthru.mainSrc;

  assertions = [
    {
      msg = "modRoot's own go.mod must be present";
      ok = builtins.pathExists "${ms}/app-full/go.mod";
    }
    {
      msg = "sibling replace target ../internal/aws must be in mainSrc";
      ok = builtins.pathExists "${ms}/internal/aws/go.mod";
    }
    {
      msg = "sibling replace target ../internal/common must be in mainSrc";
      ok = builtins.pathExists "${ms}/internal/common/go.mod";
    }
    {
      msg = "non-replace sibling app-partial must NOT be in mainSrc";
      ok = !(builtins.pathExists "${ms}/app-partial");
    }
    {
      msg = "non-replace sibling app-replace must NOT be in mainSrc";
      ok = !(builtins.pathExists "${ms}/app-replace");
    }
    {
      msg = "doCheck=false mainSrc must NOT include sibling dirs (no hash churn)";
      ok = !(builtins.pathExists "${msNoCheck}/internal");
    }
  ];

  failures = lib.filter (a: !a.ok) assertions;
in
if failures != [ ] then
  throw "mainsrc-replace-test: ${lib.concatMapStringsSep "; " (a: a.msg) failures}"
else
  runCommand "mainsrc-replace-test" { } ''
    echo "mainsrc-replace-test: ${toString (lib.length assertions)} assertions passed"
    touch $out
  ''
