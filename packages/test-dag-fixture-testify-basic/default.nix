# DAG mode fixture test: test-only third-party dep (testify/assert).
#
# Spawns nix-build with --option plugin-files so the go-nix-plugin is
# available during evaluation. Requires recursive-nix.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-testify-basic-unsupported" { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-testify-basic requires go-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go-nix-plugin;
    nix = pkgs.nixVersions.latest;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "testify-basic-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-jfyOzY3bhiTD5GZKF9aIGAYL2Bequp76/LGLc0LFFGQ=";
      nativeBuildInputs = [
        go
        pkgs.cacert
      ];
      dontUnpack = true;
      buildPhase = ''
        export HOME=$TMPDIR
        export GOMODCACHE=$out
        cd ${go2nixSrc}/tests/fixtures/testify-basic
        go mod download
      '';
      installPhase = "true";
    };
  in
  pkgs.runCommand "test-dag-fixture-testify-basic"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building testify-basic fixture (DAG mode, doCheck=true) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/fixtures/testify-basic/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/testify-basic
      echo "PASS: testify-basic" > $out
    ''
