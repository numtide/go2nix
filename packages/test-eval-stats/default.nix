# Eval-time regression guard for nix/dag/*.nix.
#
# Asserts deterministic NIX_SHOW_STATS counts (not cpuTime — counts are
# machine-independent) for instantiating torture-project/dag-app-full.nix
# stay below thresholds. Catches refactors that accidentally allocate
# more thunks/calls (e.g., a let-hoist that creates a thunk-per-iteration
# instead of sharing).
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-eval-stats-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "test-eval-stats requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    go = pkgs.go_1_26;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    tortureModules = pkgs.stdenvNoCC.mkDerivation {
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
  pkgs.runCommand "test-eval-stats"
    {
      nativeBuildInputs = [
        nix
        pkgs.jq
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      GOMODCACHE=${tortureModules} \
      NIX_SHOW_STATS=1 NIX_SHOW_STATS_PATH=$TMPDIR/stats.json \
        nix-instantiate \
          -I nixpkgs=${nixpkgsPath} \
          --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
          ${go2nixSrc}/tests/fixtures/torture-project/dag-app-full.nix \
        > /dev/null

      echo "=== NIX_SHOW_STATS (torture-project/dag-app-full.nix) ==="
      jq '{nrFunctionCalls, nrPrimOpCalls, nrThunks, nrLookups, nrOpUpdates, cpuTime}' $TMPDIR/stats.json

      fail=0
      check() {
        local key="$1" max="$2"
        val=$(jq -r ".$key" $TMPDIR/stats.json)
        if [ "$val" -ge "$max" ]; then
          echo "FAIL: $key = $val >= threshold $max"
          fail=1
        else
          echo "  OK: $key = $val < threshold $max"
        fi
      }

      # Baseline @ 13b4e98: 611,001 / 327,242 / 957,299. ~15% headroom.
      check nrFunctionCalls 700000
      check nrPrimOpCalls   380000
      check nrThunks        1100000

      if [ "$fail" -ne 0 ]; then
        cat >&2 <<'EOF'

      Eval-stats threshold(s) exceeded for torture-project/dag-app-full.nix.
      If this is an intentional cost increase, bump the thresholds in
      packages/test-eval-stats/default.nix with justification.
      EOF
        exit 1
      fi
      echo "PASS: eval stats within thresholds" > $out
    ''
