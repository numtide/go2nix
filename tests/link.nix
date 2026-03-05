# Test: end-to-end build of app-partial binary.
# Compiles third-party packages (mkGoPackageSet), local packages, and links.
let
  pkgs = import <nixpkgs> { };
  go2nixLib = import ../lib.nix { };
  go = pkgs.go;
  go2nix = import ../go/go2nix/package.nix { inherit pkgs; };

  # Third-party package set from lockfile.
  packageSet = go2nixLib.mkGoPackageSet {
    goLock = /tmp/test-lockfile.toml;
    inherit go go2nix pkgs;
  };

  stdlib = go2nixLib.buildGoStdlib {
    inherit go;
    inherit (pkgs) runCommandCC;
  };

  # Source trees for the torture test.
  tortureSrc = /home/aldo/Dev/numtide/anthropic/go2nix-torture/torture;

  # Local packages in dependency order (common first, then packages that depend on it).
  localPackages = [
    {
      importPath = "github.com/numtide/go2nix-torture/torture/internal/common";
      srcDir = "${tortureSrc}/internal/common";
    }
    {
      importPath = "github.com/numtide/go2nix-torture/torture/internal/aws";
      srcDir = "${tortureSrc}/internal/aws";
    }
    {
      importPath = "github.com/numtide/go2nix-torture/torture/internal/conflict-a";
      srcDir = "${tortureSrc}/internal/conflict-a";
    }
    {
      importPath = "github.com/numtide/go2nix-torture/torture/internal/crypto";
      srcDir = "${tortureSrc}/internal/crypto";
    }
    {
      importPath = "github.com/numtide/go2nix-torture/torture/internal/db";
      srcDir = "${tortureSrc}/internal/db";
    }
  ];

  mainPackage = {
    importPath = "github.com/numtide/go2nix-torture/torture/app-partial/cmd/app-partial";
    srcDir = "${tortureSrc}/app-partial/cmd/app-partial";
  };

  allThirdPartyDeps = builtins.attrValues packageSet;

  binary =
    pkgs.runCommandCC "app-partial"
      {
        nativeBuildInputs = [
          go
          go2nix
          pkgs.jq
        ];
      }
      ''
        export HOME=$NIX_BUILD_TOP

        # --- Build full importcfg: stdlib + all third-party packages ---
        cat "${stdlib}/importcfg" > "$NIX_BUILD_TOP/importcfg"

        ${builtins.concatStringsSep "\n" (
          map (dep: ''
            find "${dep}" -name '*.a' | while read -r pkgp; do
              relpath="''${pkgp#"${dep}/"}"
              pkgname="''${relpath%.a}"
              echo "packagefile $pkgname=$pkgp"
            done >> "$NIX_BUILD_TOP/importcfg"
          '') allThirdPartyDeps
        )}

        # --- Compile local packages ---
        localdir="$NIX_BUILD_TOP/local-pkgs"
        mkdir -p "$localdir"

        ${builtins.concatStringsSep "\n" (
          map (pkg: ''
            echo "Compiling local: ${pkg.importPath}"
            filesjson=$(go2nix list-files "${pkg.srcDir}")
            gofiles=$(echo "$filesjson" | jq -r '.go_files[]')

            embedflag=""
            hasEmbed=$(echo "$filesjson" | jq -r '.embed_cfg // empty')
            if [ -n "$hasEmbed" ]; then
              echo "$filesjson" | jq '.embed_cfg' > "$NIX_BUILD_TOP/embedcfg-local.json"
              embedflag="-embedcfg=$NIX_BUILD_TOP/embedcfg-local.json"
            fi

            mkdir -p "$localdir/$(dirname "${pkg.importPath}")"
            cd "${pkg.srcDir}"
            go tool compile \
              -importcfg "$NIX_BUILD_TOP/importcfg" \
              -p "${pkg.importPath}" \
              -trimpath="$NIX_BUILD_TOP" \
              $embedflag \
              -pack \
              -o "$localdir/${pkg.importPath}.a" \
              $gofiles

            echo "packagefile ${pkg.importPath}=$localdir/${pkg.importPath}.a" >> "$NIX_BUILD_TOP/importcfg"
          '') localPackages
        )}

        # --- Compile main package ---
        echo "Compiling main: ${mainPackage.importPath}"
        filesjson=$(go2nix list-files "${mainPackage.srcDir}")
        gofiles=$(echo "$filesjson" | jq -r '.go_files[]')

        mkdir -p "$localdir/$(dirname "${mainPackage.importPath}")"
        cd "${mainPackage.srcDir}"
        go tool compile \
          -importcfg "$NIX_BUILD_TOP/importcfg" \
          -p main \
          -trimpath="$NIX_BUILD_TOP" \
          -pack \
          -o "$localdir/${mainPackage.importPath}.a" \
          $gofiles

        # --- Link ---
        mkdir -p "$out/bin"
        go tool link \
          -importcfg "$NIX_BUILD_TOP/importcfg" \
          -o "$out/bin/app-partial" \
          "$localdir/${mainPackage.importPath}.a"
      '';

in
binary
