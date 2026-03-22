# DAG mode build test: yubikey-agent (pure Go, no cgo).
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
  pkgs.runCommand "test-dag-package-yubikey-agent-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-package-yubikey-agent requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.latest;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    src = pkgs.fetchFromGitHub {
      owner = "FiloSottile";
      repo = "yubikey-agent";
      rev = "v0.1.6";
      hash = "sha256-Knk1ipBOzjmjrS2OFUMuxi1TkyDcSYlVKezDWT//ERY=";
    };

    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "yubikey-agent-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-KLnOu5gx2O8yEIJGkTRd3dZI9gQSiXC9WsRNkxdkzxA=";
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
  pkgs.runCommand "test-dag-package-yubikey-agent"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building yubikey-agent (DAG mode) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/packages/yubikey-agent/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/yubikey-agent --help
      echo "PASS: yubikey-agent" > $out
    ''
