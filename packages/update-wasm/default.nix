# Builds the WASM plugin and copies it to nix/dag/go2nix.wasm.
#
# Usage: nix run .#update-wasm
{ pkgs }:
let
  go2nix-wasm = import ../go2nix-wasm { inherit pkgs; };
in
pkgs.writeShellApplication {
  name = "update-wasm";
  text = ''
    target="nix/dag/go2nix.wasm"
    cp "${go2nix-wasm}/go2nix.wasm" "$target"
    chmod 644 "$target"
    echo "Updated $target"
  '';
}
