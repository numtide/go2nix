{ pkgs, ... }:

let
  inherit (pkgs) lib stdenv rustPlatform pkg-config cmake boost nlohmann_json nixVersions;
  nixComponents = nixVersions.nix_2_34.libs;

  core = rustPlatform.buildRustPackage {
    pname = "go2nix-nix-plugin-core";
    version = "0.1.0";
    src = ./rust;
    cargoLock.lockFile = ./rust/Cargo.lock;
    doCheck = false;
  };
in
stdenv.mkDerivation {
  pname = "go2nix-nix-plugin";
  version = "0.1.0";

  src = ./plugin;

  nativeBuildInputs = [
    pkg-config
    cmake
  ];

  buildInputs = [
    nixComponents.nix-expr
    nixComponents.nix-store
    boost
    nlohmann_json
  ];

  cmakeFlags = [
    "-DRUST_LIB_DIR=${core}/lib"
  ];

  meta = {
    description = "Nix plugin for resolving Go module dependencies";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
