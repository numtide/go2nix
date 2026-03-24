# Test: default mode with modRoot != "." (go.mod in app/ subdirectory).
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "modroot-nested";
  version = "0.0.1";
  src = ./.;
  goLock = ./app/go2nix.toml;
  modRoot = "app";
}
