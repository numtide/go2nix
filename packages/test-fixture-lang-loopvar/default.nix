# Regression test for -lang threading to non-root local packages.
#
# Builds tests/fixtures/lang-loopvar (go 1.21) under the dag builder and
# runs the binary, which asserts pre-1.22 shared-loopvar semantics in a
# non-root subpackage. Requires recursive-nix.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-lang-loopvar-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-lang-loopvar requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-lang-loopvar"
    {
      nativeBuildInputs = [
        nix
        pkgs.go
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building lang-loopvar fixture (dag mode, go 1.21) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/lang-loopvar/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      $result/bin/lang-loopvar

      echo "=== Verifying //go:debug source directive is honoured ==="
      go version -m $result/bin/lang-loopvar | tee /tmp/modinfo
      grep -q "DefaultGODEBUG=.*panicnil=1" /tmp/modinfo \
        || { echo "FAIL: DefaultGODEBUG missing panicnil=1 from //go:debug"; exit 1; }

      echo "PASS: lang-loopvar" > $out
    ''
