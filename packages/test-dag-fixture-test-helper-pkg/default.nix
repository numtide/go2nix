# DAG mode fixture test: test-only local package (testutil helper).
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
  pkgs.runCommand "test-dag-fixture-test-helper-pkg-unsupported" { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-test-helper-pkg requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.latest;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-test-helper-pkg"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building test-helper-pkg fixture (DAG mode, doCheck=true) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/test-helper-pkg/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/test-helper-pkg
      echo "PASS: test-helper-pkg" > $out
    ''
