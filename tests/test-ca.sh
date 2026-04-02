#!/usr/bin/env bash
# Test CA derivation support by standing up a local nix daemon with
# ca-derivations enabled, building the xtest-local-dep fixture, and
# tearing everything down.
#
# Usage: ./tests/test-ca.sh
#
# Requires: nix, socat, and the go2nix-nix-plugin to be buildable.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

# --- Resolve dependencies ---
plugin=$(nix build "$repo_root#go2nix-nix-plugin" --no-link --print-out-paths)
nixpkgs_path=$(nix eval --raw nixpkgs#path)

echo "plugin: $plugin"
echo "nixpkgs: $nixpkgs_path"

# --- Set up local store + daemon ---
ca_store=$(mktemp -d)
socket="$ca_store/daemon.sock"

cleanup() {
  if [[ -n ${socat_pid:-} ]]; then
    kill "$socat_pid" 2>/dev/null || true
    wait "$socat_pid" 2>/dev/null || true
  fi
  rm -rf "$ca_store" 2>/dev/null || true
}
trap cleanup EXIT

cat >"$ca_store/daemon.sh" <<SCRIPT
#!/usr/bin/env bash
exec nix daemon --stdio \
  --option experimental-features "nix-command ca-derivations" \
  --option sandbox false \
  --option allow-import-from-derivation true \
  --store "local?root=$ca_store"
SCRIPT
chmod +x "$ca_store/daemon.sh"

socat UNIX-LISTEN:"$socket",fork EXEC:"$ca_store/daemon.sh" &
socat_pid=$!
sleep 1

echo "=== daemon listening on $socket (PID $socat_pid) ==="

# --- Build the CA fixture ---
echo "=== Building xtest-local-dep (contentAddressed) ==="
result=$(
  NIX_REMOTE="unix://$socket?root=$ca_store" \
    nix-build "$repo_root/tests/fixtures/xtest-local-dep/dag-ca.nix" \
    -I "nixpkgs=$nixpkgs_path" \
    --option extra-experimental-features 'ca-derivations' \
    --option sandbox false \
    --option allow-import-from-derivation true \
    --option plugin-files "$plugin/lib/nix/plugins/libgo2nix_plugin.so" \
    --option substituters 'https://cache.nixos.org https://cache.numtide.com https://nix-community.cachix.org' \
    --option trusted-public-keys 'cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= cache.numtide.com-1:bf/T+iRCVpNgNStXCXTiUMsdMfEfhaQzExC9NG28oIg= nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=' \
    --no-out-link
)

echo "=== Build succeeded: $result ==="

# --- Run the binary ---
"$ca_store/$result/bin/xtest-local-dep"

echo "=== PASS: xtest-local-dep-ca ==="
