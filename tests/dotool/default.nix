# Test: buildGoApplication with local cgo package (xkbcommon via pkg-config).
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
  src = pkgs.fetchgit {
    url = "https://git.sr.ht/~geb/dotool";
    rev = "180af21c46dcc848d93dbec2644c011f4eea1592";
    hash = "sha256-KI3vA45/MvFRV8Fr3Q4yd/argDy1PpFHCT3KA9VDP80=";
  };
  goLock = ./go2nix.toml;
  pname = "dotool";
  version = "1.6";
  nativeBuildInputs = [ pkgs.pkg-config pkgs.libxkbcommon ];
}
