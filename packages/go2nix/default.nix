# go2nix CLI — bootstrap build using standard buildGoModule.
#
# go2nix cannot use its own builders (which depend on go2nix for compile-package).
# Once built, go2nix is passed to the builders for all other Go projects.
{ pkgs }:
let
  buildGoModule = pkgs.buildGoModule.override { go = pkgs.go_1_26; };
in
buildGoModule {
  pname = "go2nix";
  version = "0-unstable";

  src = pkgs.lib.sources.cleanSource ../../go/go2nix;

  subPackages = [ "cmd/go2nix" ];

  vendorHash = "sha256-rUoUq91b6knN5soMbjgssc1Zi4UuLTprBujhuXrhUVU=";

  meta = {
    description = "Go Build — Nix-native Go package compiler";
    mainProgram = "go2nix";
  };
}
