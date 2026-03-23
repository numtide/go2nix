# default mode fixture test: modRoot + local replace directives (monorepo).
#
# Builds torture-project/app-full which uses modRoot="app-full" and has
# replace directives pointing to 14 sibling modules under internal/*.
# Exercises the hard case where local replace targets live outside modRoot.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-torture-app-full-unsupported" { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-torture-app-full requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.latest;
    go = pkgs.go_1_26;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    # Pre-populate GOMODCACHE: the plugin runs go list with GOPROXY=off,
    # so all ~497 third-party modules must be available locally.
    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "torture-app-full-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-uQKbuVSzWJhqbvPwi1KL5OKlYpPjHoA437m7zgQlrbA=";
      nativeBuildInputs = [
        go
        pkgs.cacert
      ];
      dontUnpack = true;
      buildPhase = ''
        export HOME=$TMPDIR
        export GOMODCACHE=$out
        cd ${go2nixSrc}/tests/fixtures/torture-project/app-full
        go mod download
      '';
      installPhase = "true";
    };
  in
  pkgs.runCommand "test-dag-fixture-torture-app-full"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building torture app-full fixture (default mode, modRoot=app-full) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/fixtures/torture-project/dag-app-full.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/app-full
      echo "PASS: torture-app-full" > $out
    ''
