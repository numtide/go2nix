# Test: buildGoApplicationDynamicMode with local cgo package (xkbcommon via pkg-config).
#
# Requires: recursive-nix, ca-derivations, dynamic-derivations experimental features.
# Requires: Nix >= 2.34 (v4 derivation JSON format).
# Run: nix-build tests/packages/dotool/dynamic.nix
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
  pname = "dotool";
  version = "1.6";
  goLock = ./go2nix.toml;
  src = pkgs.fetchgit {
    url = "https://git.sr.ht/~geb/dotool";
    rev = "180af21c46dcc848d93dbec2644c011f4eea1592";
    hash = "sha256-KI3vA45/MvFRV8Fr3Q4yd/argDy1PpFHCT3KA9VDP80=";
  };
  packageOverrides = {
    "git.sr.ht/~geb/dotool/xkb" = {
      nativeBuildInputs = [
        pkgs.pkg-config
        pkgs.libxkbcommon
      ];
    };
  };
}
