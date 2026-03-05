# Test: buildGoBinary with multiple cgo deps (gpgme pkg-config + btrfs headers).
let
  pkgs = import <nixpkgs> { };
  go2nixLib = import ../../lib.nix { };
  go = pkgs.go;
  go2nix = import ../../go/go2nix/package.nix { inherit pkgs; };
in
go2nixLib.buildGoBinary {
  src = pkgs.fetchFromGitHub {
    owner = "containers";
    repo = "skopeo";
    rev = "v1.22.0";
    hash = "sha256-ERMOquT8ke/4urC6V0To+jJPeBICohHXL9YcCmGLST4=";
  };
  goLock = ./go2nix.toml;
  pname = "skopeo";
  version = "1.22.0";
  subPackages = [ "cmd/skopeo" ];
  inherit go go2nix pkgs;
  packageOverrides = {
    "github.com/proglottis/gpgme" = {
      nativeBuildInputs = [ pkgs.pkg-config pkgs.gpgme ];
    };
    "go.podman.io/storage/drivers/btrfs" = {
      nativeBuildInputs = [ pkgs.btrfs-progs ];
    };
  };
}
