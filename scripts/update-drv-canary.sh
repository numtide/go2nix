#!/usr/bin/env bash
# Regenerate packages/test-drv-canary/expected.txt from the current tree.
#
# Run when a change is meant to move derivations; the header of
# packages/test-drv-canary/default.nix lists what the paths depend on.
# Commit the result with that change.
set -euo pipefail
cd "$(dirname "$0")/.."

# expected.txt holds x86_64-linux paths whatever the host is.
actual=$(nix build --no-link --print-out-paths .#packages.x86_64-linux.test-drv-canary.actual)

{
  echo "# Expected application .drv paths (x86_64-linux) for test-drv-canary."
  echo "# Written by ./scripts/update-drv-canary.sh; default.nix next to this"
  echo "# file says when to regenerate."
  cat "$actual"
} >packages/test-drv-canary/expected.txt

echo "Updated packages/test-drv-canary/expected.txt"
cat "$actual"
