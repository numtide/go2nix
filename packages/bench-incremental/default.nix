# bench-incremental — measures rebuild time at different dep-graph depths
# and edit types. Separate module so the go2nix CLI package doesn't ship
# benchmark tooling and stays vendorHash-stable if this grows deps.
{ pkgs }:
let
  buildGoModule = pkgs.buildGoModule.override { go = pkgs.go_1_26; };
in
buildGoModule {
  pname = "bench-incremental";
  version = "0-unstable";
  src = pkgs.lib.sources.cleanSource ../../go/bench-incremental;
  vendorHash = null;
  meta.mainProgram = "bench-incremental";
}
