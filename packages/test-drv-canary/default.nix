# Semantic-stability canary for nix/dag/*.nix.
#
# A pure refactor of the Nix-side derivation builders must produce
# identical .drv paths (same derivationStrict input). Any change means
# the refactor altered an env key, attr value, or input — i.e., semantic
# drift, even if the build result happens to be the same.
#
# Regenerate expected.txt with ./scripts/update-drv-canary.sh after any
# change to go/go2nix/, packages/go2nix-nix-plugin/, nix/dag/hooks/, or
# the nixpkgs flake input. A change to nix/dag/*.nix alone should NOT
# require updating this file — that's the point of the canary.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-drv-canary-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "test-drv-canary requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    go = pkgs.go_1_26;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    mkGoModCache =
      {
        name,
        modDir,
        outputHash,
      }:
      pkgs.stdenvNoCC.mkDerivation {
        inherit name outputHash;
        outputHashMode = "recursive";
        outputHashAlgo = "sha256";
        nativeBuildInputs = [
          go
          pkgs.cacert
        ];
        dontUnpack = true;
        buildPhase = ''
          export HOME=$TMPDIR
          export GOMODCACHE=$out
          cd ${go2nixSrc}/${modDir}
          go mod download
        '';
        installPhase = "true";
      };

    testifyModules = mkGoModCache {
      name = "testify-basic-gomodcache";
      modDir = "tests/fixtures/testify-basic";
      outputHash = "sha256-jfyOzY3bhiTD5GZKF9aIGAYL2Bequp76/LGLc0LFFGQ=";
    };
    tortureModules = mkGoModCache {
      name = "torture-app-full-gomodcache";
      modDir = "tests/fixtures/torture-project/app-full";
      outputHash = "sha256-uQKbuVSzWJhqbvPwi1KL5OKlYpPjHoA437m7zgQlrbA=";
    };

    # Produces the actual <name> <drvPath> lines. Exposed via passthru so
    # update-drv-canary.sh can build it directly even when the diff fails.
    actual =
      pkgs.runCommand "test-drv-canary-actual"
        {
          nativeBuildInputs = [ nix ];
          requiredSystemFeatures = [ "recursive-nix" ];
        }
        ''
          export HOME=$(mktemp -d)
          export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
          PLUGIN="${plugin}/lib/nix/plugins/libgo2nix_plugin.so"

          inst() {
            local name="$1" gomodcache="$2" file="$3"
            drv=$(GOMODCACHE="$gomodcache" nix-instantiate \
              -I nixpkgs=${nixpkgsPath} \
              --option plugin-files "$PLUGIN" \
              "${go2nixSrc}/$file" 2>/dev/null)
            echo "$name $drv"
          }

          {
            inst testify-basic    "${testifyModules}" tests/fixtures/testify-basic/dag.nix
            inst xtest-local-dep  "$TMPDIR/empty-gmc" tests/fixtures/xtest-local-dep/dag.nix
            inst cgo-internal-test "$TMPDIR/empty-gmc" tests/fixtures/cgo-internal-test/dag.nix
            inst torture-app-full "${tortureModules}" tests/fixtures/torture-project/dag-app-full.nix
          } > $out
        '';
  in
  pkgs.runCommand "test-drv-canary"
    {
      passthru = { inherit actual; };
    }
    ''
      grep -v '^#' ${./expected.txt} > expected.txt
      if ! diff -u expected.txt ${actual}; then
        cat >&2 <<'EOF'

      FAIL: .drv hashes changed.

      If you changed go/go2nix/, packages/go2nix-nix-plugin/, nix/dag/hooks/,
      or the nixpkgs flake input, regenerate with:
        ./scripts/update-drv-canary.sh

      If you ONLY changed nix/dag/*.nix or nix/helpers.nix, this is a
      regression: pure refactors must produce identical .drv paths.
      EOF
        exit 1
      fi
      echo "PASS: .drv hashes match canary" > $out
    ''
