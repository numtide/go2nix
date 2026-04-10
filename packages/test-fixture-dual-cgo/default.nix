# default mode fixture test: scope-level goEnv.CGO_ENABLED reaches the
# eval-time resolver so dual `//go:build cgo` / `//go:build !cgo` packages
# pick the !cgo file and link against the CGO_ENABLED=0 stdlib.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-dual-cgo-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-dual-cgo requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-dual-cgo"
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

      echo "=== Building dual-cgo fixture (scope-level goEnv.CGO_ENABLED=0) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/dual-cgo/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      mode=$($result/bin/dual-cgo)
      echo "binary output: $mode"
      [ "$mode" = "nocgo" ] || { echo "FAIL: expected 'nocgo', got '$mode'"; exit 1; }

      file $result/bin/dual-cgo | tee /dev/stderr | grep -q "statically linked" \
        || { echo "FAIL: dual-cgo should be statically linked under CGO_ENABLED=0"; exit 1; }

      echo "PASS: dual-cgo" > $out
    ''
