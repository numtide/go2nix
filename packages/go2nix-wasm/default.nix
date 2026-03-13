# Build the go2nix WASM plugin.
#
# Usage: nix run .#update-wasm
{ pkgs }:
pkgs.rustPlatform.buildRustPackage {
  pname = "go2nix-wasm";
  version = "0.1.0";
  src = ../../rust;
  cargoLock = {
    lockFile = ../../rust/Cargo.lock;
    outputHashes = {
      "nix-wasm-rust-0.1.0" = "sha256-erpUp1QmCHT26JSmLvWsfgXg6EGNuBchniMMTJs3q7s=";
    };
  };
  CARGO_BUILD_TARGET = "wasm32-unknown-unknown";
  nativeBuildInputs = [
    pkgs.lld
    pkgs.binaryen
  ];
  buildPhase = ''
    cargo build --release --target wasm32-unknown-unknown
  '';
  installPhase = ''
    mkdir -p $out
    wasm-opt -O3 -o $out/go2nix.wasm \
      target/wasm32-unknown-unknown/release/go2nix_wasm.wasm
  '';
  doCheck = false;
}
