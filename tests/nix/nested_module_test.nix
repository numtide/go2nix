# tests/nix/nested_module_test.nix — regression for pkgSrc/mainSrc leaking
# nested-module subtrees.
#
# tests/fixtures/modroot-nested/app/nested-module/ has its own go.mod, so
# go list stops there and its files are never compiled. The mainSrc and
# per-package pkgSrc filters must drop the whole subtree; otherwise touching
# a nested-module file would invalidate the parent package's compile drv.
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
  app = scope.buildGoApplication {
    pname = "nested-module-test";
    version = "0.0.1";
    src = ../fixtures/modroot-nested;
    goLock = ../fixtures/modroot-nested/app/go2nix.toml;
    modRoot = "app";
  };
  ms = app.passthru.mainSrc;

  assertions = [
    {
      msg = "modRoot's own go.mod must be present";
      ok = builtins.pathExists "${ms}/app/go.mod";
    }
    {
      msg = "main.go must be present";
      ok = builtins.pathExists "${ms}/app/main.go";
    }
    {
      msg = "internal/util (a real local package) must be present";
      ok = builtins.pathExists "${ms}/app/internal/util/util.go";
    }
    {
      msg = "nested-module subtree must NOT be in mainSrc";
      ok = !(builtins.pathExists "${ms}/app/nested-module");
    }
  ];

  failures = lib.filter (a: !a.ok) assertions;
in
if failures != [ ] then
  throw "nested-module-test: ${lib.concatMapStringsSep "; " (a: a.msg) failures}"
else
  runCommand "nested-module-test" { } ''
    echo "nested-module-test: ${toString (lib.length assertions)} assertions passed"
    touch $out
  ''
