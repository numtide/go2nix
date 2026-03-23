# Benchmark: eval-time comparison of go2nix DAG mode vs dynamic mode.
#
# Measures nix-instantiate cost only (no builds). This isolates the
# evaluation overhead: plugin invocation for DAG mode vs expression
# setup for dynamic mode.
#
# Run: nix run .#benchmark-eval
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

  # GOMODCACHE for DAG mode (plugin runs go list with GOPROXY=off).
  goModules = pkgs.stdenvNoCC.mkDerivation {
    name = "benchmark-eval-gomodcache";
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

  # go2nix DAG mode expression.
  dagExpr = pkgs.writeText "bench-eval-dag.nix" ''
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

  # go2nix dynamic mode expression.
  dynamicExpr = pkgs.writeText "bench-eval-dynamic.nix" ''
    { srcPath ? ${fixturePath} }:
    let
      pkgs = import <nixpkgs> { system = "${system}"; };
      go2nixLib = import ${go2nixSrc}/lib.nix {};
      goEnv = go2nixLib.mkGoEnv {
        go = pkgs.go_1_26;
        go2nix = import ${go2nixSrc}/packages/go2nix { inherit pkgs; };
        nixPackage = pkgs.nixVersions.latest;
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
  name = "benchmark-eval";
  runtimeInputs = [
    pkgs.nixVersions.latest
    pkgs.hyperfine
    pkgs.coreutils
    go
  ];
  text = ''
    set -euo pipefail

    WARMUP=''${BENCH_EVAL_WARMUP:-2}
    RUNS=''${BENCH_EVAL_RUNS:-5}

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
          echo "ERROR: go2nix-nix-plugin not available on ${system}."
          exit 1
        ''
    }

    RESULTS_DIR="''${BENCH_RESULTS_ROOT:-.bench-results}/benchmark-eval"
    mkdir -p "$RESULTS_DIR"

    echo "=== Eval benchmark: go2nix DAG vs dynamic ==="
    echo "  GOMODCACHE=$GOMODCACHE"
    echo "  warmup: $WARMUP  runs: $RUNS"
    echo "  results: $RESULTS_DIR"
    echo ""

    hyperfine \
      --warmup "$WARMUP" --runs "$RUNS" \
      --export-json "$RESULTS_DIR/eval.json" \
      --export-markdown "$RESULTS_DIR/eval.md" \
      -n "go2nix DAG (instantiate)" \
        "GOMODCACHE=$GOMODCACHE nix-instantiate $NIXPKGS_OPT $PLUGIN_OPT ${dagExpr}" \
      -n "go2nix dynamic (instantiate)" \
        "nix-instantiate $NIXPKGS_OPT --option extra-experimental-features 'dynamic-derivations ca-derivations recursive-nix' ${dynamicExpr}"

    echo ""
    echo "=== Results ==="
    cat "$RESULTS_DIR/eval.md"
  '';
}
