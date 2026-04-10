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
        pkgs.go
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

      output=$($result/bin/cgo-internal-test); echo "$output"

      echo "=== Asserting srcOverlay won over source-tree embeds (cgo + raw paths) ==="
      banner=$(echo "$output" | sed -n 2p)
      version=$(echo "$output" | sed -n 3p)
      [ "$banner" = "hello-from-overlay" ] \
        || { echo "FAIL: adder.Banner() = '$banner', want hello-from-overlay (cgo srcOverlay path)"; exit 1; }
      [ "$version" = "v1.2.3-overlay" ] \
        || { echo "FAIL: stamp.Version = '$version', want v1.2.3-overlay (rawGoCompile srcOverlay path)"; exit 1; }

      echo "=== Asserting purebin (built after a cgo subPackage) is statically linked ==="
      [ "$($result/bin/purebin)" = "pure" ] || { echo "FAIL: purebin output"; exit 1; }
      file $result/bin/purebin | tee /dev/stderr | grep -q "statically linked" \
        || { echo "FAIL: purebin should be statically linked (cgo marker leaked)"; exit 1; }
      file $result/bin/cgo-internal-test | tee /dev/stderr | grep -q "dynamically linked" \
        || { echo "FAIL: cgo-internal-test should be dynamically linked (uses cgo)"; exit 1; }

      echo "=== Asserting BuildInfo.Path is the per-binary main package path ==="
      go version -m $result/bin/purebin | tee /dev/stderr \
        | grep -qE '^[[:space:]]*path[[:space:]]+example.com/cgo-internal-test/cmd/purebin$' \
        || { echo "FAIL: purebin BuildInfo.Path should be the main package import path"; exit 1; }
      go version -m $result/bin/cgo-internal-test | tee /dev/stderr \
        | grep -qE '^[[:space:]]*path[[:space:]]+example.com/cgo-internal-test$' \
        || { echo "FAIL: root binary BuildInfo.Path should be the module path"; exit 1; }

      echo "=== Asserting cgowork temp dir does not leak into compiled archives ==="
      drv=$(nix-instantiate ${go2nixSrc}/tests/fixtures/cgo-internal-test/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" 2>/dev/null)
      for d in $(nix-store -q --references "$drv" | grep -- -golocal-); do
        a=$(find "$(nix-store -q --outputs "$d")" -name '*.a')
        if grep -aq cgo_work_ "$a"; then
          echo "FAIL: $a embeds cgowork temp dir (non-reproducible)"; exit 1
        fi
      done

      echo "PASS: cgo-internal-test" > $out
    ''
