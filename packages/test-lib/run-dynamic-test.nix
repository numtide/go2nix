# Shared test runner for buildGoApplicationExperimental (dynamic mode).
#
# Exercises pkg/resolve and the recursive-nix + ca-derivations +
# dynamic-derivations chain. nix-build of dynamic.nix produces a text-mode
# CA output that IS a .drv file; nix-store -r on that path realises the
# final binary.
{
  pkgs,
  testName,
  dynamicFile,
  checkCommand ? "$result/bin/${testName} --help",
}:
let
  nix = pkgs.nixVersions.nix_2_34;
  nixpkgsPath = pkgs.path;
in
pkgs.runCommand "test-dynamic-package-${testName}"
  {
    nativeBuildInputs = [ nix ];
    requiredSystemFeatures = [
      "recursive-nix"
      "ca-derivations"
    ];
    meta.platforms = pkgs.lib.platforms.linux;
  }
  ''
    export HOME=$(mktemp -d)
    export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix ca-derivations dynamic-derivations"

    echo "=== Building ${testName} (dynamic mode) ==="
    wrapper=$(nix-build ${dynamicFile} \
      -I nixpkgs=${nixpkgsPath} \
      --no-out-link)

    result=$(nix-store -r "$wrapper")

    ${checkCommand}
    echo "PASS: ${testName} (dynamic mode)" > $out
  ''
