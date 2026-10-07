# default mode fixture test: `toolchain go1.99.0` in the main go.mod and in the
# go.mod of a module replaced with a directory. `go build` accepts both under
# GOTOOLCHAIN=local, and ignores the dependency's line under any setting.
# The build runs `go` below both files: the two package compiles, the compile
# of main in link-binary and, under doCheck, the test runner's recompile of
# internal/b for the external test of internal/a. It only succeeds if none of
# those tries to switch to the toolchain the lines name.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-toolchain-line-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-toolchain-line requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-toolchain-line"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      echo "=== Building toolchain-line fixture (doCheck=true) ==="
      result=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/toolchain-line/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      fail() { echo "FAIL: $*"; exit 1; }

      [ "$($result/bin/toolchain-line)" = "ok" ] || fail "unexpected output"

      echo "PASS: toolchain-line" > $out
    ''
