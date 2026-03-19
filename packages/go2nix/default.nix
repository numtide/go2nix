# go2nix CLI — bootstrap build using standard buildGoModule.
#
# go2nix cannot use its own builders (which depend on go2nix for compile-package).
# Once built, go2nix is passed to the builders for all other Go projects.
{ pkgs }:
pkgs.buildGoModule {
  pname = "go2nix";
  version = "0-unstable";

  src = ../../go/go2nix;

  subPackages = [ "cmd/go2nix" ];

  vendorHash = "sha256-xC7TFbsTp0YsIXhRO9LLZwLTW5rU9GRFTMOBbTMSbns=";

  meta = {
    description = "Go Build — Nix-native Go package compiler";
    mainProgram = "go2nix";
  };
}
