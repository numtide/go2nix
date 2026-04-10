# default mode fixture test: contentAddressed = true.
#
# Builds the dag-ca.nix variant of xtest-local-dep with ca-derivations
# enabled. Asserts the per-package golocal-* drvs declare both `out` and
# `iface` outputs (the CA early-cutoff iface split) and the binary runs.
#
# This relies on the inner recursive-nix store accepting CA derivations.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-xtest-local-dep-ca-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-xtest-local-dep-ca requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-xtest-local-dep-ca"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [
        "recursive-nix"
        "ca-derivations"
      ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix ca-derivations"

      echo "=== Building xtest-local-dep (contentAddressed) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/xtest-local-dep/dag-ca.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option extra-experimental-features 'nix-command ca-derivations' \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      echo "=== Asserting per-package drvs declare iface output (CA split) ==="
      top_drv=$(nix-instantiate ${go2nixSrc}/tests/fixtures/xtest-local-dep/dag-ca.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option extra-experimental-features 'nix-command ca-derivations' \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so")
      sample_local=$(nix-store -q --references "$top_drv" | grep "golocal-" | head -1)
      [ -n "$sample_local" ] || { echo "FAIL: no golocal-* drv in closure"; exit 1; }
      outputs=$(nix derivation show "$sample_local^*" \
        | grep -oE '"(out|iface)"' | sort -u | tr -d '"' | tr '\n' ' ')
      [ "$outputs" = "iface out " ] || { echo "FAIL: expected outputs 'iface out ', got '$outputs' on $sample_local"; exit 1; }

      "$result/bin/xtest-local-dep"

      echo "PASS: xtest-local-dep-ca (per-package outputs: $outputs)" > $out
    ''
