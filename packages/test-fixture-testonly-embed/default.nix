# default mode fixture test: a test-only-local package with its own
# `//go:embed` in *_test.go targeting a non-testdata file. doCheck must
# pass — the precise mainSrc filter must include internal/testutil/fixture.json.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-testonly-embed-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-testonly-embed requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-testonly-embed"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      echo "=== Building testonly-embed fixture (doCheck=true) ==="
      result=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/testonly-embed/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      output=$($result/bin/app)
      [ "$output" = "hello world" ] || { echo "FAIL: unexpected output: '$output'"; exit 1; }
      echo "PASS: testonly-embed" > $out
    ''
