# Test: default mode with a test-only local package (internal/testutil
# is only imported from internal/app/app_test.go). doCheck exercises the
# plugin's testLocalPackages output and the dag builder's union of those
# into localPackages so the testrunner has an .a archive for the helper.
#
# Also: cmd/unbuilt + internal/unbuiltdep are deliberately NOT in the
# subPackages closure (default = ["."], and the root main.go does not
# import them). The testrunner must skip cmd/unbuilt's test rather than
# fail trying to compile it against an importcfg that lacks unbuiltdep.
#
# internal/brokenembed is likewise out-of-closure and has a //go:embed
# pattern with no match in the source closure. The testrunner must defer
# embed-pattern resolution until after the LocalArchives filter so this
# package is skipped, not fatal.
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "test-helper-pkg";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
}
