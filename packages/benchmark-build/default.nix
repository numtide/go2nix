# Benchmark: build-time comparison of buildGoModule vs go2nix vs go2nix experimental.
#
# Measures three phases using hyperfine:
#   1. Clean build (delete outputs, build from scratch)
#   2. Cached rebuild (no-op, everything in store)
#   3. Rebuild after deterministic source change
#
# Run: nix run .#benchmark-build
{
  flake,
  pkgs,
  system,
  ...
}:

let
  nixpkgsPath = pkgs.path;
  go2nixSrc = flake;
  go = pkgs.go_1_26;

  hasPlugin =
    flake.packages.${system} ? go2nix-nix-plugin;
  plugin =
    if hasPlugin
    then flake.packages.${system}.go2nix-nix-plugin
    else null;
  pluginPath =
    if plugin != null
    then "${plugin}/lib/nix/plugins/libgo2nix_plugin.so"
    else "";

  fixturePath = "${go2nixSrc}/tests/fixtures/torture-project";

  # Pre-populate GOMODCACHE: the plugin runs go list with GOPROXY=off.
  goModules = pkgs.stdenvNoCC.mkDerivation {
    name = "benchmark-app-full-gomodcache";
    outputHashMode = "recursive";
    outputHashAlgo = "sha256";
    outputHash = "sha256-uQKbuVSzWJhqbvPwi1KL5OKlYpPjHoA437m7zgQlrbA=";
    nativeBuildInputs = [ go pkgs.cacert ];
    dontUnpack = true;
    buildPhase = ''
      export HOME=$TMPDIR
      export GOMODCACHE=$out
      cd ${fixturePath}/app-full
      go mod download
    '';
    installPhase = "true";
  };

  # --- Nix expressions for each contender ---

  # buildGoModule: standard nixpkgs monolithic approach.
  bgmExpr = pkgs.writeText "bench-buildGoModule.nix" ''
    { srcPath ? ${fixturePath} }:
    let pkgs = import <nixpkgs> { system = "${system}"; }; in
    pkgs.buildGoModule {
      pname = "torture-bgm";
      version = "0.0.1";
      src = srcPath;
      modRoot = "app-full";
      vendorHash = "sha256-e46RmcsCTgMqbl1vP9Z51fFBrq5xtL/H0xAa3OG5GlY=";
      subPackages = [ "cmd/app-full" ];
    }
  '';

  # go2nix mode: per-package build graph via go2nix-nix-plugin.
  dagExpr = pkgs.writeText "bench-dag.nix" ''
    { srcPath ? ${fixturePath} }:
    let
      pkgs = import <nixpkgs> { system = "${system}"; };
      go2nixLib = import ${go2nixSrc}/lib.nix {};
      goEnv = go2nixLib.mkGoEnv {
        go = pkgs.go_1_26;
        go2nix = import ${go2nixSrc}/packages/go2nix { inherit pkgs; };
        inherit (pkgs) callPackage;
      };
    in
    goEnv.buildGoApplication {
      src = srcPath;
      modRoot = "app-full";
      goLock = "''${srcPath}/app-full/go2nix.toml";
      pname = "torture-dag";
      version = "0.0.1";
      subPackages = [ "./cmd/app-full" ];
    }
  '';

  # go2nix experimental mode: recursive-nix + CA derivations.
  dynamicExpr = pkgs.writeText "bench-dynamic.nix" ''
    { srcPath ? ${fixturePath} }:
    let
      pkgs = import <nixpkgs> { system = "${system}"; };
      go2nixLib = import ${go2nixSrc}/lib.nix {};
      goEnv = go2nixLib.mkGoEnv {
        go = pkgs.go_1_26;
        go2nix = import ${go2nixSrc}/packages/go2nix { inherit pkgs; };
        nixPackage = pkgs.nixVersions.nix_2_34;
        inherit (pkgs) callPackage;
      };
    in
    goEnv.buildGoApplicationExperimental {
      src = srcPath;
      modRoot = "app-full";
      goLock = "''${srcPath}/app-full/go2nix.toml";
      pname = "torture-dynamic";
      subPackages = [ "./cmd/app-full" ];
    }
  '';

in
pkgs.writeShellApplication {
  name = "benchmark-build";
  runtimeInputs = [
    pkgs.nixVersions.nix_2_34
    pkgs.coreutils
    pkgs.hyperfine
    pkgs.jq
    go
  ];
  text = ''
    set -euo pipefail

    CLEAN_RUNS=''${BENCH_CLEAN_RUNS:-1}
    CACHED_WARMUP=''${BENCH_CACHED_WARMUP:-1}
    CACHED_RUNS=''${BENCH_CACHED_RUNS:-3}
    SRC_CHANGE_RUNS=''${BENCH_SRC_CHANGE_RUNS:-3}

    NIXPKGS_OPT="-I nixpkgs=${nixpkgsPath}"
    GOMODCACHE="${goModules}"
    export GOMODCACHE

    ${
      if hasPlugin then
        ''
          PLUGIN_OPT="--option plugin-files ${pluginPath}"
        ''
      else
        ''
          echo "ERROR: go2nix-nix-plugin not available on ${system}. go2nix benchmarks require it."
          exit 1
        ''
    }

    # --- Results directory ---
    RESULTS_DIR="''${BENCH_RESULTS_ROOT:-.bench-results}/benchmark-build"
    mkdir -p "$RESULTS_DIR"

    # --- Metadata ---
    REVISION="unknown"
    DIRTY="false"
    if [ -n "''${BENCH_REPO_ROOT:-}" ] && [ -d "$BENCH_REPO_ROOT/.git" ]; then
      REVISION=$(git -C "$BENCH_REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
      DIRTY=$(git -C "$BENCH_REPO_ROOT" diff --quiet 2>/dev/null && echo "false" || echo "true")
    fi
    cat > "$RESULTS_DIR/metadata.json" <<METAEOF
    {
      "name": "benchmark-build",
      "fixture": "torture-project/app-full",
      "system": "${system}",
      "nix_version": "$(nix --version 2>/dev/null || echo unknown)",
      "go_version": "$(go version 2>/dev/null || echo unknown)",
      "plugin_enabled": ${if hasPlugin then "true" else "false"},
      "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "revision": "$REVISION",
      "dirty": $DIRTY,
      "evicted_runs": $CLEAN_RUNS,
      "cached_runs": $CACHED_RUNS,
      "src_change_runs": $SRC_CHANGE_RUNS
    }
    METAEOF

    echo "=== Build benchmark: buildGoModule vs go2nix vs go2nix experimental ==="
    echo "  GOMODCACHE=$GOMODCACHE"
    echo "  results:  $RESULTS_DIR"
    echo ""

    # --- Build command helpers ---
    # shellcheck disable=SC2086
    build_bgm()     { nix-build --show-trace $NIXPKGS_OPT ${bgmExpr} "$@"; }
    # shellcheck disable=SC2086
    build_dag()     { GOMODCACHE="$GOMODCACHE" nix-build --show-trace $NIXPKGS_OPT $PLUGIN_OPT ${dagExpr} "$@"; }
    # shellcheck disable=SC2086
    build_dynamic() { nix-build --show-trace $NIXPKGS_OPT --option extra-experimental-features 'dynamic-derivations ca-derivations recursive-nix' ${dynamicExpr} "$@"; }
    export -f build_bgm build_dag build_dynamic
    export NIXPKGS_OPT GOMODCACHE PLUGIN_OPT

    # --- Collect .drv paths and their outputs for store cleanup ---
    CLEANUP_DIR=$(mktemp -d)
    trap 'rm -rf "$CLEANUP_DIR"' EXIT

    echo "--- Instantiating expressions ---"

    echo "  buildGoModule..."
    # shellcheck disable=SC2086
    BGM_DRV=$(nix-instantiate --show-trace $NIXPKGS_OPT ${bgmExpr})
    echo "  -> $BGM_DRV"

    echo "  go2nix..."
    # shellcheck disable=SC2086
    DAG_DRV=$(GOMODCACHE="$GOMODCACHE" nix-instantiate --show-trace $NIXPKGS_OPT $PLUGIN_OPT ${dagExpr})
    echo "  -> $DAG_DRV"

    echo "  go2nix experimental..."
    # shellcheck disable=SC2086
    DYN_DRV=$(nix-instantiate --show-trace $NIXPKGS_OPT --option extra-experimental-features 'dynamic-derivations ca-derivations recursive-nix' ${dynamicExpr})
    echo "  -> $DYN_DRV"
    echo ""

    # Collect output paths for cleanup
    drv_outputs_file() {
      nix-store -qR "$1" \
        | grep '\.drv$' \
        | xargs nix-store -q --outputs 2>/dev/null \
        | sort -u > "$2" || true
    }
    drv_outputs_file "$BGM_DRV" "$CLEANUP_DIR/bgm-outputs"
    drv_outputs_file "$DAG_DRV" "$CLEANUP_DIR/dag-outputs"
    drv_outputs_file "$DYN_DRV" "$CLEANUP_DIR/dyn-outputs"
    ALL_OUTPUTS_FILE="$CLEANUP_DIR/all-outputs"
    sort -u "$CLEANUP_DIR/bgm-outputs" "$CLEANUP_DIR/dag-outputs" "$CLEANUP_DIR/dyn-outputs" > "$ALL_OUTPUTS_FILE"
    export ALL_OUTPUTS_FILE

    delete_all_outputs() { xargs nix store delete < "$ALL_OUTPUTS_FILE" 2>/dev/null || true; }
    export -f delete_all_outputs

    # --- Phase 1: Build after output eviction ---
    # Deletes reachable outputs from the host store, but shared deps (go, stdenv)
    # may survive if other store paths reference them. This is an approximation;
    # true cold builds require VM-backed strict mode.
    echo "=== Phase 1: Build after output eviction ($CLEAN_RUNS run, host-store) ==="
    echo ""
    hyperfine \
      --warmup 0 --runs "$CLEAN_RUNS" \
      --export-json "$RESULTS_DIR/evicted-build.json" \
      --export-markdown "$RESULTS_DIR/evicted-build.md" \
      -n "buildGoModule (evicted)" \
        --prepare 'bash -c delete_all_outputs' \
        "nix-build $NIXPKGS_OPT ${bgmExpr} --no-out-link" \
      -n "go2nix (evicted)" \
        --prepare 'bash -c delete_all_outputs' \
        "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT ${dagExpr} --no-out-link" \
      -n "go2nix experimental (evicted)" \
        --prepare 'bash -c delete_all_outputs' \
        "nix-build $NIXPKGS_OPT --option extra-experimental-features 'dynamic-derivations ca-derivations recursive-nix' ${dynamicExpr} --no-out-link"
    echo ""

    # --- Phase 2: Cached rebuild (no-op) ---
    echo "=== Phase 2: Cached rebuild ($CACHED_RUNS runs) ==="
    echo "  Pre-building all..."
    build_bgm --no-out-link >/dev/null 2>&1
    build_dag --no-out-link >/dev/null 2>&1
    build_dynamic --no-out-link >/dev/null 2>&1
    echo "  done."
    echo ""
    hyperfine \
      --warmup "$CACHED_WARMUP" --runs "$CACHED_RUNS" \
      --export-json "$RESULTS_DIR/cached-rebuild.json" \
      --export-markdown "$RESULTS_DIR/cached-rebuild.md" \
      -n "buildGoModule (cached)" \
        "nix-build $NIXPKGS_OPT ${bgmExpr} --no-out-link" \
      -n "go2nix (cached)" \
        "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT ${dagExpr} --no-out-link" \
      -n "go2nix experimental (cached)" \
        "nix-build $NIXPKGS_OPT --option extra-experimental-features 'dynamic-derivations ca-derivations recursive-nix' ${dynamicExpr} --no-out-link"
    echo ""

    # --- Phase 3: Rebuild after source change ---
    # Copy the fixture to a temp dir, then for each hyperfine iteration:
    #   prepare: reset copy to baseline (already cached in store)
    #   command: apply a unique mutation + build
    #
    # Each iteration appends a unique comment (counter-based) so the NAR hash
    # differs every time, forcing a real rebuild. The reset step restores the
    # baseline without cumulative drift.
    echo "=== Phase 3: Rebuild after source change ($SRC_CHANGE_RUNS runs) ==="

    FIXTURE_COPY=$(mktemp -d -t bench-fixture-XXXXXX)
    trap 'rm -rf "$FIXTURE_COPY" "$CLEANUP_DIR"' EXIT
    cp -a "${fixturePath}/." "$FIXTURE_COPY/"
    chmod -R u+w "$FIXTURE_COPY"

    # Pre-build the baseline from the copy so the cache is warm.
    echo "  Pre-building baseline from fixture copy..."
    build_bgm --arg srcPath "$FIXTURE_COPY" --no-out-link >/dev/null 2>&1
    build_dag --arg srcPath "$FIXTURE_COPY" --no-out-link >/dev/null 2>&1
    build_dynamic --arg srcPath "$FIXTURE_COPY" --no-out-link >/dev/null 2>&1
    echo "  done."
    echo ""

    FIXTURE_SRC="${fixturePath}"
    MAIN_GO="$FIXTURE_COPY/app-full/cmd/app-full/main.go"
    COUNTER_FILE=$(mktemp)
    export FIXTURE_COPY FIXTURE_SRC MAIN_GO COUNTER_FILE

    # Prepare: reset fixture copy to baseline, then apply iteration N's mutation.
    # Counter resets to 0 before each contender, so iteration N of every contender
    # rebuilds from the exact same post-change source tree.
    reset_and_mutate() {
      rm -rf "''${FIXTURE_COPY:?}/"*
      cp -a "$FIXTURE_SRC/." "$FIXTURE_COPY/"
      chmod -R u+w "$FIXTURE_COPY"
      local n
      n=$(cat "$COUNTER_FILE")
      echo "$((n + 1))" > "$COUNTER_FILE"
      echo "// bench-iteration $n" >> "$MAIN_GO"
    }
    export -f reset_and_mutate

    # Run each contender separately so the counter resets between them.
    # This ensures iteration N of each contender sees the same source tree.
    echo 0 > "$COUNTER_FILE"
    hyperfine \
      --warmup 0 --runs "$SRC_CHANGE_RUNS" \
      --export-json "$RESULTS_DIR/src-change-bgm.json" \
      -n "buildGoModule (src change)" \
        --prepare 'bash -c reset_and_mutate' \
        "nix-build $NIXPKGS_OPT --arg srcPath $FIXTURE_COPY ${bgmExpr} --no-out-link"

    echo 0 > "$COUNTER_FILE"
    hyperfine \
      --warmup 0 --runs "$SRC_CHANGE_RUNS" \
      --export-json "$RESULTS_DIR/src-change-dag.json" \
      -n "go2nix (src change)" \
        --prepare 'bash -c reset_and_mutate' \
        "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT --arg srcPath $FIXTURE_COPY ${dagExpr} --no-out-link"

    echo 0 > "$COUNTER_FILE"
    hyperfine \
      --warmup 0 --runs "$SRC_CHANGE_RUNS" \
      --export-json "$RESULTS_DIR/src-change-dynamic.json" \
      -n "go2nix experimental (src change)" \
        --prepare 'bash -c reset_and_mutate' \
        "nix-build $NIXPKGS_OPT --option extra-experimental-features 'dynamic-derivations ca-derivations recursive-nix' --arg srcPath $FIXTURE_COPY ${dynamicExpr} --no-out-link"

    # Merge per-contender results into combined JSON and Markdown.
    jq -s '{ results: [ .[].results[] ] }' \
      "$RESULTS_DIR/src-change-bgm.json" \
      "$RESULTS_DIR/src-change-dag.json" \
      "$RESULTS_DIR/src-change-dynamic.json" \
      > "$RESULTS_DIR/src-change.json"

    # Generate Markdown table from merged results.
    {
      echo "| Command | Mean [s] | Min [s] | Max [s] |"
      echo "|:---|---:|---:|---:|"
      jq -r '.results[] | "| \(.command) | \(.mean | tostring | .[0:6]) | \(.min | tostring | .[0:6]) | \(.max | tostring | .[0:6]) |"' \
        "$RESULTS_DIR/src-change.json"
    } > "$RESULTS_DIR/src-change.md"
    echo ""

    # --- Summary ---
    echo "=== Results saved to $RESULTS_DIR/ ==="
    for f in "$RESULTS_DIR"/*.md; do
      echo ""
      echo "--- $(basename "$f" .md) ---"
      cat "$f"
    done
  '';
}
