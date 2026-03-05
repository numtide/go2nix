# Test: compile a single leaf package (google/uuid) via go tool compile.
let
  pkgs = import <nixpkgs> { };
  go2nixLib = import ../lib.nix { };
  go = pkgs.go;
  go2nix = import ../go/go2nix/package.nix { inherit pkgs; };

  stdlib = go2nixLib.buildGoStdlib {
    inherit go;
    inherit (pkgs) runCommandCC;
  };

  # Fetch the module source via fetchModuleProxy pattern.
  # google/uuid has no deps beyond stdlib, making it ideal for testing.
  uuidSrc = pkgs.fetchurl {
    url = "https://proxy.golang.org/github.com/google/uuid/@v/v1.6.0.zip";
    hash = "sha256-0PAvN3IX9CcC4lloTgZEHtv1FA3dzDS6m+pWA4s4pu0=";
  };

  # Compile a single package.
  uuidPkg =
    pkgs.runCommand "go-pkg-github.com-google-uuid"
      {
        nativeBuildInputs = [
          go
          go2nix
          pkgs.unzip
        ];
      }
      ''
        # Unpack module source.
        mkdir -p src
        unzip -q ${uuidSrc} -d src

        # Discover files for the target package.
        pkgDir="src/github.com/google/uuid@v1.6.0"
        files=$(go2nix list-files "$pkgDir" | ${pkgs.jq}/bin/jq -r '.go_files[]')

        # Build importcfg from stdlib.
        cp ${stdlib}/importcfg importcfg

        # Compile.
        mkdir -p $out/github.com/google
        cd "$pkgDir"
        go tool compile \
          -importcfg $NIX_BUILD_TOP/importcfg \
          -p github.com/google/uuid \
          -pack \
          -o $out/github.com/google/uuid.a \
          $files
      '';

in
uuidPkg
