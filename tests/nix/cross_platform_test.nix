# tests/nix/cross_platform_test.nix — regression for findings #10/#12.
#
# Eval-only assertions that under a cross stdenv:
#   - the scope's goEnv carries GOOS/GOARCH for the host platform
#   - the stdlib derivation runs on the build platform
#   - native scopes leave goEnv untouched (no hash churn)
#
# Does not exercise per-package compile drvs (those need the plugin); the
# scope-level goEnv is what mkCompileEnv and stdlib both consume, so
# asserting it here covers the threading.
{
  lib,
  pkgs,
  runCommand,
}:
let
  go2nix = import ../../packages/go2nix { inherit pkgs; };

  nativeScope = import ../../nix/mk-go-env.nix {
    inherit (pkgs) go callPackage;
    inherit go2nix;
  };

  # Cross to a target that differs from every supported flake system on at
  # least one of GOOS/GOARCH.
  crossPkgs = pkgs.pkgsCross.riscv64;
  crossScope = import ../../nix/mk-go-env.nix {
    inherit (pkgs) go;
    inherit go2nix;
    inherit (crossPkgs) callPackage;
  };

  assertions = [
    {
      msg = "native scope must not inject GOOS/GOARCH into goEnv";
      ok = nativeScope.goEnv == { };
    }
    {
      msg = "cross scope goEnv.GOOS must match hostPlatform";
      ok = crossScope.goEnv.GOOS == crossPkgs.stdenv.hostPlatform.go.GOOS;
    }
    {
      msg = "cross scope goEnv.GOARCH must match hostPlatform";
      ok = crossScope.goEnv.GOARCH == crossPkgs.stdenv.hostPlatform.go.GOARCH;
    }
    {
      msg = "cross stdlib must build on buildPlatform.system";
      ok = crossScope.stdlib.system == crossPkgs.stdenv.buildPlatform.system;
    }
    {
      msg = "user goEnv overrides cross-derived GOOS";
      ok =
        (import ../../nix/mk-go-env.nix {
          inherit (pkgs) go;
          inherit go2nix;
          inherit (crossPkgs) callPackage;
          goEnv.GOOS = "plan9";
        }).goEnv.GOOS == "plan9";
    }
  ];

  failures = lib.filter (a: !a.ok) assertions;
in
if failures != [ ] then
  throw "cross-platform-test: ${lib.concatMapStringsSep "; " (a: a.msg) failures}"
else
  runCommand "cross-platform-test" { } ''
    echo "cross-platform-test: ${toString (lib.length assertions)} assertions passed"
    touch $out
  ''
