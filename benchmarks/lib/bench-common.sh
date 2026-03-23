#!/usr/bin/env bash
# Shared benchmark helpers (fast local mode).
#
# These helpers are designed for host-store benchmarks where the developer
# runs `nix run .#benchmark-*` locally. They are NOT suitable for strict
# VM-mode benchmarks, which will need their own isolation primitives.
#
# Source this file from benchmark scripts: source @benchLib@/bench-common.sh

set -euo pipefail

# --- Metadata ---

# Collect benchmark metadata and write it to a JSON file.
# Also prints a human-readable summary to stdout.
# Usage: bench_metadata "benchmark-build" "$results_dir"
bench_metadata() {
  local name="${1:?benchmark name required}"
  local results_dir="${2:?results directory required}"
  local meta_file="$results_dir/metadata.json"
  local system nix_ver go_ver ts revision dirty

  system=$(nix eval --impure --expr builtins.currentSystem --raw 2>/dev/null || uname -m)
  nix_ver=$(nix --version 2>/dev/null || echo "unknown")
  go_ver=$(go version 2>/dev/null || echo "unknown")
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  revision="unknown"
  dirty="false"
  if [ -n "${BENCH_REPO_ROOT:-}" ] && [ -d "$BENCH_REPO_ROOT/.git" ]; then
    revision=$(git -C "$BENCH_REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
    dirty=$(git -C "$BENCH_REPO_ROOT" diff --quiet 2>/dev/null && echo "false" || echo "true")
  fi

  cat > "$meta_file" <<EOF
{
  "name": "$name",
  "system": "$system",
  "nix_version": "$nix_ver",
  "go_version": "$go_ver",
  "timestamp": "$ts",
  "revision": "$revision",
  "dirty": $dirty,
  "mode": "host-store"
}
EOF

  echo "=== Benchmark: $name ==="
  echo "  system:   $system"
  echo "  nix:      $nix_ver"
  echo "  go:       $go_ver"
  echo "  revision: $revision (dirty=$dirty)"
  echo "  time:     $ts"
  echo ""
}

# --- Results directory ---

# Ensure results directory exists and return its path.
# Usage: results_dir=$(bench_results_dir "benchmark-build")
bench_results_dir() {
  local name="${1:?benchmark name required}"
  local dir="${BENCH_RESULTS_ROOT:-.bench-results}/$name"
  mkdir -p "$dir"
  echo "$dir"
}

# --- Fixture copying ---

# Copy a fixture directory to a temporary location for mutation.
# Returns the path to the copy. Caller is responsible for cleanup.
# Usage: work_dir=$(bench_copy_fixture "$fixture_path")
bench_copy_fixture() {
  local src="${1:?fixture path required}"
  local dest
  dest=$(mktemp -d -t bench-fixture-XXXXXX)
  cp -a "$src/." "$dest/"
  echo "$dest"
}

# Apply a deterministic patch to a fixture copy.
# The patch file should be relative to the fixture root.
# Usage: bench_apply_patch "$work_dir" "$patch_file"
bench_apply_patch() {
  local work_dir="${1:?work directory required}"
  local patch_file="${2:?patch file required}"
  patch -d "$work_dir" -p1 < "$patch_file"
}

# Reset a fixture copy to its original state by re-copying from source.
# Usage: bench_reset_fixture "$work_dir" "$fixture_path"
bench_reset_fixture() {
  local work_dir="${1:?work directory required}"
  local src="${2:?fixture path required}"
  rm -rf "${work_dir:?}/"*
  cp -a "$src/." "$work_dir/"
}

# --- Host-store cleanup (fast local mode only) ---

# Collect all output paths from a .drv and its build closure into a file.
# This is a host-store operation unsuitable for VM-mode benchmarks.
# Usage: bench_host_drv_outputs "$drv_path" "$output_file"
bench_host_drv_outputs() {
  local drv="${1:?drv path required}"
  local outfile="${2:?output file required}"
  nix-store -qR "$drv" \
    | grep '\.drv$' \
    | xargs nix-store -q --outputs 2>/dev/null \
    | sort -u > "$outfile" || true
}

# Delete store paths listed in a file.
# This is a host-store operation unsuitable for VM-mode benchmarks.
# Usage: bench_host_delete_outputs "$file"
bench_host_delete_outputs() {
  local file="${1:?file path required}"
  if [ -f "$file" ]; then
    xargs nix store delete < "$file" 2>/dev/null || true
  fi
}

# --- Derivation counting ---

# Count derivations that need building from a .drv (dry-run).
# Usage: count=$(bench_count_to_build "$drv_path")
bench_count_to_build() {
  local drv="${1:?drv path required}"
  local dry
  dry=$(nix-store --realise "$drv" --dry-run 2>&1)
  echo "$dry" | grep -c '\.drv$' || echo "0"
}
