{ pkgs }:
with pkgs;
buildGoModule {
  pname = "go2nix";
  version = "0-unstable";

  src = lib.fileset.toSource {
    root = ./go2nix;
    fileset = lib.fileset.unions [
      ./go2nix/go.mod
      ./go2nix/go.sum
      ./go2nix/main.go
      ./go2nix/main_test.go
      ./go2nix/integration_test.go
      ./go2nix/builder
    ];
  };

  subPackages = [ "." ];

  vendorHash = "sha256-xC7TFbsTp0YsIXhRO9LLZwLTW5rU9GRFTMOBbTMSbns=";

  meta = {
    description = "Generate Nix lockfiles for Go modules";
    mainProgram = "go2nix";
  };
}
