#!/usr/bin/env bash
# Regenerate packages/test-drv-canary/expected.txt from the current tree.
#
# Run after any change to go/go2nix/, packages/go2nix-nix-plugin/,
# nix/dag/hooks/, or the nixpkgs flake input. A change to nix/dag/*.nix
# alone should NOT require running this — that's the point of the canary.
set -euo pipefail
cd "$(dirname "$0")/.."

actual=$(nix build --no-link --print-out-paths .#test-drv-canary.actual)

{
  echo "# Expected .drv hashes for the nix/dag semantic-stability canary."
  echo "# Regenerate with ./scripts/update-drv-canary.sh after any change to"
  echo "# go/go2nix/, packages/go2nix-nix-plugin/, nix/dag/hooks/, or the nixpkgs"
  echo "# flake input. A change to nix/dag/*.nix alone should NOT require"
  echo "# updating this file."
  cat "$actual"
} >packages/test-drv-canary/expected.txt

echo "Updated packages/test-drv-canary/expected.txt"
cat "$actual"
