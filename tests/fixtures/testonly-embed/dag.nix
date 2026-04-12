# Regression fixture: a test-only-local package (internal/testutil — only
# imported from *_test.go) that itself has a `//go:embed` in its own
# *_test.go targeting a non-testdata file. The precise mainSrc filter must
# include that target so doCheck can compile and run testutil's tests.
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
  pname = "testonly-embed";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  subPackages = [ "./cmd/app" ];
  doCheck = true;
}
