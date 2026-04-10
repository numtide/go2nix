# Test: default mode with a transitive C++ cgo dependency that links a real
# external library (libsnappy.so) via `#cgo pkg-config:` and packageOverrides.
#
# main.go is pure Go; internal/snap has a .cc shim calling snappy::Compress.
# Exercises packageOverrides.nativeBuildInputs → resolvePkgConfig →
# compileCgo CXXFiles → nix/dag transitive cxx=true → linkbinary -extld $CXX
# → external linker against /nix/store/...-snappy/lib/libsnappy.so. cxx-cgo
# covers the same CXX-as-extld step but with an in-tree .cc only and no
# pkg-config / external .so.
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
  pname = "cxx-pkgconfig";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  packageOverrides."example.com/cxx-pkgconfig/internal/snap" = {
    nativeBuildInputs = [
      pkgs.pkg-config
      pkgs.snappy
    ];
  };
}
