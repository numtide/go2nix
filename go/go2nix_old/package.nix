{ pkgs }:
with pkgs;
buildGoModule {
  pname = "go2nix";
  version = "0-unstable";

  src = ./.;

  subPackages = [ "." ];

  vendorHash = "sha256-kESsE+x8ca+9HL6ce9epmStvAvM13vO28iPlyLgguH8=";

  meta = {
    description = "Generate Nix lockfiles for Go modules";
    mainProgram = "go2nix";
  };
}
