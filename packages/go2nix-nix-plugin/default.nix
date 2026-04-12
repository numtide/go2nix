{ pkgs, ... }:

let
  inherit (pkgs)
    lib
    stdenv
    rustPlatform
    pkg-config
    cmake
    boost
    nlohmann_json
    nixVersions
    go
    ;
  nixComponents = nixVersions.nix_2_34.libs;

  core = rustPlatform.buildRustPackage {
    pname = "go2nix-nix-plugin-core";
    version = "0.1.0";
    src = ./rust;
    cargoLock.lockFile = ./rust/Cargo.lock;
    doCheck = true;
    # run_go_list_surfaces_mfiles_and_swig invokes the baked-in go binary,
    # which needs a writable GOCACHE/HOME in the sandbox.
    preCheck = ''
      export HOME=$TMPDIR
      export GOCACHE=$TMPDIR/gocache
    '';
    # Baked into the binary via option_env!("GO2NIX_DEFAULT_GO") so the
    # eval-time builtin never needs to realise a derivation for the Go
    # toolchain — that would be IFD.
    GO2NIX_DEFAULT_GO = "${go}/bin/go";
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
