# go2nix-testgen — built with go2nix default mode (dogfooding).
#
# Spawns nix-build with --option plugin-files so the go2nix-nix-plugin is
# available during evaluation. The result is the testgen binary.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "go2nix-testgen-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "go2nix-testgen requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "go2nix-testgen"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      result=$(nix-build ${go2nixSrc}/packages/go2nix-testgen/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      mkdir -p $out/bin
      cp $result/bin/go2nix-testgen $out/bin/
    ''
