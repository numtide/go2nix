# default mode fixture test: custom build tags.
#
# tags=["mytag"] must reach resolveGoPackages, the per-package compile
# manifest, and modinfo. internal/feature has //go:build mytag and
# //go:build !mytag twins; the binary prints "on" only if the tag was
# threaded end-to-end.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-build-tags-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-build-tags requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-build-tags"
    {
      nativeBuildInputs = [
        nix
        pkgs.go
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building build-tags fixture (default mode, tags=[mytag]) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/build-tags/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      got=$($result/bin/build-tags)
      [ "$got" = "on" ] || { echo "FAIL: got '$got', want 'on' (tags not threaded)"; exit 1; }

      echo "=== Asserting modinfo records -tags=mytag ==="
      go version -m "$result/bin/build-tags" | tee buildinfo.txt
      grep -P '^\tbuild\t-tags=mytag$' buildinfo.txt \
        || { echo "FAIL: missing 'build -tags=mytag' in modinfo"; exit 1; }

      echo "PASS: build-tags" > $out
    ''
