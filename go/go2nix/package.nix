# go2nix/package.nix — bootstrap build using standard buildGoModule.
#
# go2nix cannot use buildGoBinary (which depends on go2nix for compile-package).
# Once built, go2nix is passed to buildGoBinary for all other Go projects.
{ pkgs }:
pkgs.buildGoModule {
  pname = "go2nix";
  version = "0-unstable";

  src = ./.;

  subPackages = [ "cmd/go2nix" ];

  vendorHash = "sha256-kESsE+x8ca+9HL6ce9epmStvAvM13vO28iPlyLgguH8=";

  meta = {
    description = "Go Build — Nix-native Go package compiler";
    mainProgram = "go2nix";
  };
}
