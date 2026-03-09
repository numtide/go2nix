#!/usr/bin/env bash
# Benchmark lockfile processing: pure-Nix vs WASM plugin.
#
# Usage: bash tests/nix/bench-process-lockfile.sh [iterations]
#
# Set NIX_WASM to the path of a Nix binary with wasm-builtin support:
#   NIX_WASM=/path/to/nix bash tests/nix/bench-process-lockfile.sh

set -euo pipefail
cd "$(dirname "$0")/../.."

ITERS="${1:-5}"
TIME=$(command -v time)
NIX_WASM="${NIX_WASM:-}"

bench() {
  local label="$1" nix_bin="$2" expr="$3" n="$4" flags="${5:-}"
  echo "--- $label ---"
  for i in $(seq 1 "$n"); do
    # shellcheck disable=SC2086
    "$TIME" -f "  run $i: %e s (wall), %U s (user), %M KB (peak RSS)" \
      "$nix_bin" --eval --strict $flags --expr "$expr" > /dev/null
  done
  echo ""
}

echo "=== Lockfile processing benchmark ==="
echo ""

echo "== Pure Nix =="
echo ""

bench "Small lockfile (dotool: 2 modules, 2 packages)" \
  nix-instantiate \
  'builtins.toJSON ((import ./nix/process-lockfile.nix) ./tests/packages/dotool/go2nix.toml)' \
  "$ITERS"

bench "Large lockfile (app-full: 478 modules, 3250 packages)" \
  nix-instantiate \
  'builtins.toJSON ((import ./nix/process-lockfile.nix) ./tests/nix/app-full-go2nix.toml)' \
  "$ITERS"

# WASM benchmarks
if [ -n "$NIX_WASM" ]; then
  NIX_INST="$NIX_WASM"
elif command -v nix-instantiate &>/dev/null && nix-instantiate --eval --expr 'builtins ? wasm' 2>/dev/null | grep -q true; then
  NIX_INST="nix-instantiate"
else
  NIX_INST=""
fi

if [ -n "$NIX_INST" ]; then
  echo "== WASM plugin (${NIX_INST}) =="
  echo ""

  bench "Small lockfile (dotool: 2 modules, 2 packages)" \
    "$NIX_INST" \
    'builtins.toJSON (builtins.wasm { path = ./nix/go2nix-wasm.wasm; function = "process_lockfile"; } ./tests/packages/dotool/go2nix.toml)' \
    "$ITERS" \
    "--extra-experimental-features wasm-builtin"

  bench "Large lockfile (app-full: 478 modules, 3250 packages)" \
    "$NIX_INST" \
    'builtins.toJSON (builtins.wasm { path = ./nix/go2nix-wasm.wasm; function = "process_lockfile"; } ./tests/nix/app-full-go2nix.toml)' \
    "$ITERS" \
    "--extra-experimental-features wasm-builtin"
else
  echo "== WASM plugin =="
  echo ""
  echo "  Skipped: no WASM-enabled Nix found."
  echo "  Set NIX_WASM=/path/to/nix-instantiate to enable."
  echo ""
fi
