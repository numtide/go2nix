# Test: buildGoApplicationDynamicMode with packageOverrides for cgo (pcsclite via pkg-config).
#
# Requires: recursive-nix, ca-derivations, dynamic-derivations experimental features.
# Requires: Nix >= 2.34 (v4 derivation JSON format).
# Run: nix-build tests/packages/yubikey-agent/dynamic.nix
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
    nixPackage = pkgs.nixVersions.git;
  };
in
goEnv.buildGoApplicationDynamicMode {
  src = pkgs.fetchFromGitHub {
    owner = "FiloSottile";
    repo = "yubikey-agent";
    rev = "v0.1.6";
    hash = "sha256-Knk1ipBOzjmjrS2OFUMuxi1TkyDcSYlVKezDWT//ERY=";
  };
  goLock = ./go2nix-dynamic.toml;
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
