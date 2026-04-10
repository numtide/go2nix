# Test: default mode checkPhase for a local cgo package with internal _test.go
# files. Regression for the testrunner dropping CgoFiles/SFiles/SysoFiles when
# building the internal test archive.
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
  pname = "cgo-internal-test";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = true;
  # Root binary first (imports cgo via internal/adder), pure-Go second:
  # regression for .has_cgo marker leaking across SubPackages iterations.
  subPackages = [
    "."
    "./cmd/purebin"
  ];
  # srcOverlay regression: adder is cgo (stdenv hook path), stamp is
  # pure Go (rawGoCompile path). The overlay must win over the
  # source-tree placeholder in both.
  packageOverrides = {
    "example.com/cgo-internal-test/internal/adder" = {
      srcOverlay = pkgs.runCommand "adder-overlay" { } ''
        mkdir -p $out
        echo -n "hello-from-overlay" > $out/data.txt
      '';
    };
    "example.com/cgo-internal-test/internal/stamp" = {
      srcOverlay = pkgs.runCommand "stamp-overlay" { } ''
        mkdir -p $out
        echo -n "v1.2.3-overlay" > $out/VERSION
      '';
    };
  };
}
