# Test: buildGoApplicationDAGMode (explicit) with packageOverrides for cgo (pcsclite via pkg-config).
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplicationDAGMode {
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
