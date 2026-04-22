# tests/nix/modroot_dotslash_test.nix — regression for -trimpath rewrite
# missing when modRoot has a ./ prefix and the main package is at the
# module root (subPackages = ["."]).
#
# Without normalization, moduleRoot = "${mainSrc}/./app" — the rewrite
# key carries /./ but the compiler records the kernel-canonicalized cwd,
# so objabi's prefix match misses and mainSrc lands in pclntab. The
# disallowedReferences check on the link drv catches that; this test
# just builds the fixture so the guard runs in CI.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "modroot-dotslash-test-unsupported" { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "modroot-dotslash-test requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "modroot-dotslash-test"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      out=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build --no-out-link \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        ${go2nixSrc}/tests/fixtures/modroot-dotslash/dag.nix)

      # disallowedReferences on the link drv already enforces no mainSrc;
      # this is a belt-and-suspenders explicit check on the realised path.
      ms=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-instantiate --eval --read-write-mode --raw \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        -A passthru.mainSrc \
        ${go2nixSrc}/tests/fixtures/modroot-dotslash/dag.nix)

      refs=$(nix-store -q --references "$out")
      if echo "$refs" | grep -qF "$ms"; then
        echo "FAIL: $out references mainSrc ($ms)" >&2
        echo "$refs" >&2
        exit 1
      fi

      "$out"/bin/modroot-dotslash > /dev/null
      echo "modroot-dotslash-test: built, no mainSrc reference, binary runs" > $out
    ''
