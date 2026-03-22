# DAG mode build test: vinegar (puregotk, no CGO for GTK libs).
#
# Spawns nix-build with --option plugin-files so the go-nix-plugin is
# available during evaluation. Requires recursive-nix.
#
# Linux-only: the Nix plugin is platform-specific.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go-nix-plugin) then
  pkgs.runCommand "test-dag-package-vinegar-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-package-vinegar requires go-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go-nix-plugin;
    nix = pkgs.nixVersions.latest;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    src = pkgs.fetchFromGitHub {
      owner = "vinegarhq";
      repo = "vinegar";
      rev = "v1.9.3";
      hash = "sha256-0MNUkfhbsvOJdN89VGTuf3zHUFhimiCNuoY47V03Cgo=";
    };

    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "vinegar-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-cJyYqJmAr8Zb/rjcm36iDeQ6wF/QUXpLjPqa0SP/hz8=";
      nativeBuildInputs = [
        go
        pkgs.cacert
      ];
      dontUnpack = true;
      buildPhase = ''
        export HOME=$TMPDIR
        export GOMODCACHE=$out
        cd ${src}
        go mod download
      '';
      installPhase = "true";
    };
  in
  pkgs.runCommand "test-dag-package-vinegar"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building vinegar (DAG mode) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/packages/vinegar/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      test -x $result/bin/vinegar
      echo "PASS: vinegar" > $out
    ''
