# default mode fixture test: a sibling-replace package whose xtest imports
# a test-only sub-package that itself imports the package-under-test. With an
# internal test file present, that sub-package only appears in `go list -test`
# as the recompiled `[X.test]` variant — never the bare path. parse_test_packages
# must surface it under the bare path so mainSrc and testLocalPackages cover it.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-sibling-testonly-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-sibling-testonly requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-sibling-testonly"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      echo "=== Building sibling-testonly fixture (doCheck=true) ==="
      result=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/sibling-testonly/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      output=$($result/bin/app)
      [ "$output" = "hello world" ] || { echo "FAIL: unexpected output: '$output'"; exit 1; }
      echo "PASS: sibling-testonly" > $out
    ''
