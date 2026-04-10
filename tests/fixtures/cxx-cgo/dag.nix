# Test: default mode with a *transitive* C++ cgo dependency.
#
# main.go is pure Go; internal/cxxdep has a .cc file that uses std::string.
# Exercises nix/dag's per-subPackage closure walk that sets cxx=true (so
# linkbinary picks $CXX, not $CC, for -extld), plus compileCgo's CXX path
# (compile/cgo.go compileCFiles for CXXFiles). Linking with CC would fail
# on undefined libstdc++ symbols, so the build itself is the regression.
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
  pname = "cxx-cgo";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
}
