# go2nix-testgen built with go2nix default mode (dogfooding).
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../go2nix { inherit pkgs; };
  goEnv = import ../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "go2nix-testgen";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
}
