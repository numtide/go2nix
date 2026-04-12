{ pkgs, ... }:
let
  inherit (pkgs) rustPlatform clippy go;
in
rustPlatform.buildRustPackage {
  pname = "cargo-clippy-go2nix-nix-plugin";
  version = "0.1.0";
  src = ../go2nix-nix-plugin/rust;
  cargoLock.lockFile = ../go2nix-nix-plugin/rust/Cargo.lock;

  nativeBuildInputs = [ clippy ];
  GO2NIX_DEFAULT_GO = "${go}/bin/go";

  buildPhase = ''
    runHook preBuild
    cargo clippy --all-targets -- -D warnings
    runHook postBuild
  '';

  doCheck = false;
  installPhase = "touch $out";
}
