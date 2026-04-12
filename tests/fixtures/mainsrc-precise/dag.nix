# Regression fixture for the file-precise mainSrc filter:
#   - testdata/ reaches the testrunner (greet_test.go reads it via os.ReadFile)
#   - build-time //go:embed (schema.json) is in mainSrc
#   - test-only //go:embed (testdata/golden.json) is in mainSrc
#   - README.md and unrelated.yaml are NOT in mainSrc
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
  pname = "mainsrc-precise";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  subPackages = [ "./cmd/app" ];
  doCheck = true;
}
