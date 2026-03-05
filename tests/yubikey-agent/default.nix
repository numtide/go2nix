# Test: buildGoApplication with packageOverrides for cgo (pcsclite via pkg-config).
let
  pkgs = import <nixpkgs> { };
  go = pkgs.go;
  go2nix = import ../../go/go2nix/package.nix { inherit pkgs; };
  goEnv = import ../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  src = pkgs.fetchFromGitHub {
    owner = "FiloSottile";
    repo = "yubikey-agent";
    rev = "v0.1.6";
    hash = "sha256-Knk1ipBOzjmjrS2OFUMuxi1TkyDcSYlVKezDWT//ERY=";
  };
  goLock = ./go2nix.toml;
  pname = "yubikey-agent";
  version = "0.1.6";
  packageOverrides = {
    "github.com/go-piv/piv-go/piv" = {
      nativeBuildInputs = [
        pkgs.pkg-config
        pkgs.pcsclite
      ];
    };
  };
}
