# Benchmark: shared dependency reuse + per-local-package isolation in DAG mode.
#
# Demonstrates two properties:
#   1. Third-party package derivations are reused between app-full and app-partial
#   2. Per-local-package source filtering prevents app-partial rebuilds when only
#      app-full's dependencies change
#
# Run: nix run .#benchmark-build-cross-app-isolation
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

  hasPlugin = flake.packages.${system} ? go2nix-nix-plugin;
  plugin =
    if hasPlugin
    then flake.packages.${system}.go2nix-nix-plugin
    else null;
  pluginPath =
    if plugin != null
    then "${plugin}/lib/nix/plugins/libgo2nix_plugin.so"
    else "";

  fixturePath = "${go2nixSrc}/tests/fixtures/torture-project";

  # GOMODCACHE for both apps (app-full is a superset of app-partial's deps).
  goModules = pkgs.stdenvNoCC.mkDerivation {
    name = "benchmark-cross-app-gomodcache";
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

  # DAG expression for app-full.
  dagExprFull = pkgs.writeText "bench-cross-full.nix" ''
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
      pname = "torture-full";
      version = "0.0.1";
      subPackages = [ "./cmd/app-full" ];
    }
  '';

  # DAG expression for app-partial.
  dagExprPartial = pkgs.writeText "bench-cross-partial.nix" ''
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
      modRoot = "app-partial";
      goLock = "''${srcPath}/app-partial/go2nix.toml";
      pname = "torture-partial";
      version = "0.0.1";
      subPackages = [ "./cmd/app-partial" ];
    }
  '';

in
pkgs.writeShellApplication {
  name = "benchmark-build-cross-app-isolation";
  runtimeInputs = [
    pkgs.nixVersions.latest
    pkgs.hyperfine
    pkgs.coreutils
    go
  ];
  text = ''
    set -euo pipefail

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
          echo "ERROR: go2nix-nix-plugin not available on ${system}. DAG mode benchmarks require it."
          exit 1
        ''
    }

    RESULTS_DIR="''${BENCH_RESULTS_ROOT:-.bench-results}/benchmark-build-cross-app-isolation"
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
      "name": "benchmark-build-cross-app-isolation",
      "fixture": "torture-project (app-full + app-partial)",
      "system": "${system}",
      "nix_version": "$(nix --version 2>/dev/null || echo unknown)",
      "go_version": "$(go version 2>/dev/null || echo unknown)",
      "plugin_enabled": ${if hasPlugin then "true" else "false"},
      "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "revision": "$REVISION",
      "dirty": $DIRTY
    }
    METAEOF

    echo "=== DAG Mode: Cross-App Isolation Benchmark ==="
    echo "  GOMODCACHE=$GOMODCACHE"
    echo "  results:  $RESULTS_DIR"
    echo ""

    # --- Phase 1: Build both apps to populate all caches ---
    echo "--- Phase 1: Building app-full then app-partial (initial) ---"
    echo "  (app-partial runs second and benefits from shared store state)"
    echo ""

    # shellcheck disable=SC2086
    hyperfine --runs 1 --style basic \
      --export-json "$RESULTS_DIR/initial-app-full.json" \
      --export-markdown "$RESULTS_DIR/initial-app-full.md" \
      --command-name "app-full (initial)" \
      "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT ${dagExprFull} --no-out-link 2>&1"

    # shellcheck disable=SC2086
    hyperfine --runs 1 --style basic \
      --export-json "$RESULTS_DIR/initial-app-partial.json" \
      --export-markdown "$RESULTS_DIR/initial-app-partial.md" \
      --command-name "app-partial (initial, shared deps cached)" \
      "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT ${dagExprPartial} --no-out-link 2>&1"

    echo ""

    # --- Phase 2: Mutate internal/web (used by app-full only) ---
    echo "--- Phase 2: Mutating internal/web (app-full dependency only) ---"

    FIXTURE_COPY=$(mktemp -d -t bench-cross-app-XXXXXX)
    trap 'rm -rf "$FIXTURE_COPY"' EXIT
    cp -a "${fixturePath}/." "$FIXTURE_COPY/"

    # Build baseline from the copy.
    echo "  Pre-building baseline from copy..."
    # shellcheck disable=SC2086
    GOMODCACHE="$GOMODCACHE" nix-build $NIXPKGS_OPT $PLUGIN_OPT \
      --arg srcPath "$FIXTURE_COPY" ${dagExprFull} --no-out-link >/dev/null 2>&1
    # shellcheck disable=SC2086
    GOMODCACHE="$GOMODCACHE" nix-build $NIXPKGS_OPT $PLUGIN_OPT \
      --arg srcPath "$FIXTURE_COPY" ${dagExprPartial} --no-out-link >/dev/null 2>&1
    echo "  done."

    # Apply mutation.
    WEB_GO="$FIXTURE_COPY/internal/web/web.go"
    echo "// bench-cross-app-mutation" >> "$WEB_GO"
    echo "  Mutated: internal/web/web.go"
    echo "  (internal/web is used by app-full but NOT by app-partial)"
    echo ""

    # --- Phase 3: Rebuild after mutation ---
    echo "--- Phase 3: Rebuild after mutating internal/web ---"
    echo ""

    # app-full: must rebuild (source changed in a dependency).
    # shellcheck disable=SC2086
    hyperfine --runs 1 --style basic \
      --export-json "$RESULTS_DIR/rebuild-app-full.json" \
      --export-markdown "$RESULTS_DIR/rebuild-app-full.md" \
      --command-name "app-full (rebuild after mutation)" \
      "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT --arg srcPath $FIXTURE_COPY ${dagExprFull} --no-out-link 2>&1"

    # app-partial: should NOT rebuild. Count derivations to verify.
    # shellcheck disable=SC2086
    PARTIAL_DRV=$(GOMODCACHE="$GOMODCACHE" nix-instantiate $NIXPKGS_OPT $PLUGIN_OPT \
      --arg srcPath "$FIXTURE_COPY" ${dagExprPartial} 2>/dev/null | tail -1)
    PARTIAL_DRY=$(nix-store --realise "$PARTIAL_DRV" --dry-run 2>&1)
    PARTIAL_TO_BUILD=$(echo "$PARTIAL_DRY" | grep -c '\.drv$' || echo "0")

    if [ "$PARTIAL_TO_BUILD" -gt 0 ]; then
      echo "  WARNING: app-partial has $PARTIAL_TO_BUILD derivations to rebuild:"
      echo "$PARTIAL_DRY" | grep '\.drv$' || true
    else
      echo "  app-partial: 0 derivations to rebuild (isolation verified)"
    fi

    # shellcheck disable=SC2086
    hyperfine --runs 1 --style basic \
      --export-json "$RESULTS_DIR/rebuild-app-partial.json" \
      --export-markdown "$RESULTS_DIR/rebuild-app-partial.md" \
      --command-name "app-partial (rebuild after mutation)" \
      "GOMODCACHE=$GOMODCACHE nix-build $NIXPKGS_OPT $PLUGIN_OPT --arg srcPath $FIXTURE_COPY ${dagExprPartial} --no-out-link 2>&1"

    echo ""

    # --- Summary ---
    echo "=== Cross-App Isolation Results ==="
    echo "  app-partial derivations to rebuild: $PARTIAL_TO_BUILD"
    echo ""
    echo "  Per-local-package derivations: each local package's source is filtered"
    echo "  individually. internal/web change doesn't affect app-partial's packages"
    echo "  -> zero derivations to rebuild."
    echo ""
    echo "  Results saved to $RESULTS_DIR/"
  '';
}
