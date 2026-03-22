# DAG mode fixture test: xtest recompilation with local dependent packages.
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
  pkgs.runCommand "test-dag-fixture-xtest-local-dep-unsupported" { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-xtest-local-dep requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.latest;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-xtest-local-dep"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building xtest-local-dep fixture (DAG mode, doCheck=true) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/xtest-local-dep/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/xtest-local-dep
      echo "PASS: xtest-local-dep" > $out
    ''
