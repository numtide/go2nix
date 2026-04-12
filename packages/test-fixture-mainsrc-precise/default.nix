# default mode fixture test: file-precise mainSrc with doCheck=true.
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
  pkgs.runCommand "test-dag-fixture-mainsrc-precise-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-mainsrc-precise requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-mainsrc-precise"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      echo "=== Building mainsrc-precise fixture (doCheck=true) ==="
      result=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/mainsrc-precise/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      output=$($result/bin/app)
      expected=$(printf 'hello\n{"ok":true}\n')
      [ "$output" = "$expected" ] || { echo "FAIL: unexpected output: '$output'"; exit 1; }
      echo "PASS: mainsrc-precise" > $out
    ''
