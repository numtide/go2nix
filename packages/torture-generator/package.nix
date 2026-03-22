{ buildGoModule }:

buildGoModule {
  pname = "go2nix-torture-generator";
  version = "0.0.1";
  src = ./.;
  vendorHash = null;
}
