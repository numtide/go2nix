# Derivation-stability canary for nix/dag/*.nix and the resolver plugin.
#
# Instantiates four fixtures with the plugin built from this tree and
# compares the application .drv paths with expected.txt. A change that
# claims to leave every derivation alone (a refactor of nix/dag/*.nix or
# nix/helpers.nix, a plugin change that returns the same graph) must
# produce identical paths: a different path means an env key, an attribute
# or an input moved, even if the build result happens to be the same.
#
# The paths also depend on the nixpkgs flake input (stdenv, Go),
# nix/dag/hooks/, nix/stdlib.nix, the four fixtures' sources and what the
# plugin returns for them. After a change to one of those, or a change to
# nix/dag that is meant to move derivations, regenerate expected.txt with
# ./scripts/update-drv-canary.sh and commit it with the change.
#
# They do not depend on go/go2nix/: the fixtures are instantiated with a
# stand-in for the CLI. The real one has its store path in every compile
# derivation, so each CLI change would move all four paths and say nothing
# about nix/dag.
#
# expected.txt holds x86_64-linux paths.
{
  flake,
  pkgs,
  system,
  ...
}:
if system != "x86_64-linux" then
  pkgs.runCommand "test-drv-canary-unsupported" { } ''
    echo "test-drv-canary: expected.txt holds x86_64-linux paths; build .#packages.x86_64-linux.test-drv-canary" >&2
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
          # torture-project has a go.work: without this, go fetches the
          # workspace's build list instead of app-full's.
          export GOWORK=off
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

    standIn = ''derivation { name = "go2nix-stand-in"; system = "${system}"; builder = "/bin/sh"; }'';

    # Produces the actual <name> <drvPath> lines. Exposed via passthru so
    # update-drv-canary.sh can build it directly even when the diff fails.
    #
    # Evaluating drvPath read-only computes the same store paths as an
    # instantiation without writing them, and the dummy store keeps
    # /nix/store as the store directory, so this needs neither a daemon
    # nor recursive-nix.
    actual =
      pkgs.runCommand "test-drv-canary-actual"
        {
          nativeBuildInputs = [ nix ];
        }
        ''
          export HOME=$(mktemp -d)
          mkdir $TMPDIR/empty-gmc
          PLUGIN="${plugin}/lib/nix/plugins/libgo2nix_plugin.so"

          inst() {
            local name="$1" gomodcache="$2" file="$3"
            drv=$(GOMODCACHE="$gomodcache" nix-instantiate \
              --eval --raw --readonly-mode --store dummy:// \
              -I nixpkgs=${nixpkgsPath} \
              --option plugin-files "$PLUGIN" \
              --arg go2nix '${standIn}' \
              -A drvPath \
              "${go2nixSrc}/$file")
            echo "$name $drv"
          }

          {
            inst testify-basic     "${testifyModules}" tests/fixtures/testify-basic/dag.nix
            inst xtest-local-dep   "$TMPDIR/empty-gmc" tests/fixtures/xtest-local-dep/dag.nix
            inst cgo-internal-test "$TMPDIR/empty-gmc" tests/fixtures/cgo-internal-test/dag.nix
            inst torture-app-full  "${tortureModules}" tests/fixtures/torture-project/dag-app-full.nix
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

      FAIL: .drv paths changed.

      If the change is meant to leave derivations alone (a refactor of
      nix/dag/*.nix or nix/helpers.nix, a plugin change that returns the
      same graph), this is a regression.

      If it is meant to move them (the nixpkgs flake input, nix/dag/hooks/,
      nix/stdlib.nix, a fixture, what the plugin returns, new builder
      behaviour), regenerate and commit the result with the change:
        ./scripts/update-drv-canary.sh
      Without an x86_64-linux builder, the + lines above are the new
      contents of expected.txt.
      EOF
        exit 1
      fi
      echo "PASS: .drv paths match canary" > $out
    ''
