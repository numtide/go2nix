# default mode build test: nwg-drawer (cgo with GTK3/GTK Layer Shell).
#
# Spawns nix-build with --option plugin-files so the go2nix-nix-plugin is
# available during evaluation. Requires recursive-nix.
#
# Linux-only: the Nix plugin is platform-specific.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-package-nwg-drawer-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-package-nwg-drawer requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    src = pkgs.fetchFromGitHub {
      owner = "nwg-piotr";
      repo = "nwg-drawer";
      rev = "v0.7.4";
      hash = "sha256-yKRh2kAWg8GJjEJ/yCJ88JoJSgYR3c3RafeYU3z3pNU=";
    };

    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "nwg-drawer-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-IMb8G/HdP6P1IKf+rZVWi6RGPGyJKmb64umwAZ5bUbA=";
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
  pkgs.runCommand "test-dag-package-nwg-drawer"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building nwg-drawer (default mode) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/packages/nwg-drawer/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/nwg-drawer --help
      echo "PASS: nwg-drawer" > $out
    ''
