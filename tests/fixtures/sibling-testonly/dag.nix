# Regression fixture for sibling-replace test-only sub-packages:
#   - sib/ is a local-replace target in the build closure (cmd/app imports it)
#   - sib/helper_test.go imports sib/testutil, which is NOT in any build closure
#   - mainSrc must include sib/testutil/testutil.go so the testrunner can
#     compile the test-only package when running sib's tests
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
  pname = "sibling-testonly";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  subPackages = [ "./cmd/app" ];
  doCheck = true;
}
