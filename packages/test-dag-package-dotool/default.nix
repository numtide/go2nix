# DAG mode build test: dotool (cgo package with xkbcommon via pkg-config).
#
# Spawns nix-build with --option plugin-files so the go-nix-plugin is
# available during evaluation. Requires recursive-nix.
#
# Linux-only: the Nix plugin is platform-specific.
{
  inputs,
  flake,
  pkgs,
  system,
  ...
}:
if !(inputs.go-nix-plugin.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-package-dotool-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-package-dotool requires go-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = inputs.go-nix-plugin.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_33;
    go = pkgs.go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    src = pkgs.fetchgit {
      url = "https://git.sr.ht/~geb/dotool";
      rev = "180af21c46dcc848d93dbec2644c011f4eea1592";
      hash = "sha256-KI3vA45/MvFRV8Fr3Q4yd/argDy1PpFHCT3KA9VDP80=";
    };

    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "dotool-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-M5FjxDF/cC4OjSUy/k3a5DK6K21Mv4w/NBYbxCovpps=";
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
  pkgs.runCommand "test-dag-package-dotool"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building dotool (DAG mode) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/packages/dotool/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_nix_plugin.so" \
        --no-out-link)

      $result/bin/dotool --version
      echo "PASS: dotool" > $out
    ''
