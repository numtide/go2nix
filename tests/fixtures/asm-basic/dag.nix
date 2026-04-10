# Test: default mode with a Go-assembly (.s) source file.
#
# Exercises compileWithAsm: the two-pass `go tool asm -gensymabis` →
# `go tool compile -symabis -asmhdr` → `go tool asm` → pack-append flow,
# plus the asm arch-define flags. The bodyless decl in add_amd64.go fails
# to compile if the .s file is dropped, so the build itself is the check.
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
  pname = "asm-basic";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
}
