# default mode fixture test: cgo package with internal _test.go files.
#
# Spawns nix-build with --option plugin-files so the go2nix-nix-plugin is
# available during evaluation. Requires recursive-nix.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-cgo-internal-test-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-cgo-internal-test requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-cgo-internal-test"
    {
      nativeBuildInputs = [
        nix
        pkgs.file
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building cgo-internal-test fixture (default mode, doCheck=true) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/cgo-internal-test/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/cgo-internal-test

      echo "=== Asserting purebin (built after a cgo subPackage) is statically linked ==="
      [ "$($result/bin/purebin)" = "pure" ] || { echo "FAIL: purebin output"; exit 1; }
      file $result/bin/purebin | tee /dev/stderr | grep -q "statically linked" \
        || { echo "FAIL: purebin should be statically linked (cgo marker leaked)"; exit 1; }
      file $result/bin/cgo-internal-test | tee /dev/stderr | grep -q "dynamically linked" \
        || { echo "FAIL: cgo-internal-test should be dynamically linked (uses cgo)"; exit 1; }

      echo "PASS: cgo-internal-test" > $out
    ''
