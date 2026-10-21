# experimental mode fixture test: a per-build CGO_ENABLED and per-build tags
# reach the standard library `go2nix resolve` is given.
#
# Builds tests/fixtures/stdlib-per-build through dynamic.nix in the same
# four ways as test-fixture-stdlib-per-build and inspects each binary the
# same way. The experimental builder runs no tests, so there is no test
# binary to look at.
{
  flake,
  pkgs,
  ...
}:
let
  nix = pkgs.nixVersions.nix_2_34;
  nixpkgsPath = pkgs.path;
in
pkgs.runCommand "test-dynamic-fixture-stdlib-per-build"
  {
    nativeBuildInputs = [
      nix
      pkgs.file
      pkgs.go
    ];
    requiredSystemFeatures = [
      "recursive-nix"
      "ca-derivations"
    ];
    meta.platforms = pkgs.lib.platforms.linux;
  }
  ''
    export HOME=$(mktemp -d)
    export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix ca-derivations dynamic-derivations"

    source ${../test-fixture-stdlib-per-build/inspect-binary.sh}

    fixture=${flake}/tests/fixtures/stdlib-per-build/dynamic.nix

    # check <variant> <output> <linkage> <CGO_ENABLED> <-tags or ""> <net cgo> <os/user cgo> <tzdata>
    check() {
      variant=$1
      echo "=== $variant (dynamic mode) ==="
      wrapper=$(nix-build "$fixture" -I nixpkgs=${nixpkgsPath} --argstr variant "$variant" --no-out-link)
      result=$(nix-store -r "$wrapper")
      inspectBinary "$result/bin/stdlib-per-build" "''${@:2}"
    }

    check default cgo   "dynamically linked" 1 ""                         yes yes no
    check cgo0    nocgo "statically linked"  0 ""                         no  no  no
    check tags    cgo   "dynamically linked" 1 netgo,osusergo,timetzdata  no  no  yes
    check cgo1    cgo   "dynamically linked" 1 ""                         yes yes no

    echo "PASS: stdlib-per-build (dynamic mode)" > $out
  ''
