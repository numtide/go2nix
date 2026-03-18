# Re-export the go-nix-plugin from the flake input.
# Provides builtins.resolveGoPackages for DAG mode.
# Linux-only: the Nix plugin uses dlopen and is platform-specific.
{
  inputs,
  pkgs,
  system,
  ...
}:
inputs.go-nix-plugin.packages.${system}.go2nix-nix-plugin
  or (pkgs.runCommand "go-nix-plugin-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "go-nix-plugin is only available on Linux" >&2
    exit 1
  '')
