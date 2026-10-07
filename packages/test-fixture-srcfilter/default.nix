# default mode fixture test: srcFilter: real tree + predicate == pre-filtered src (drv-identical), unfiltered differs.
#
# Spawns nix-build with --option plugin-files so the go2nix-nix-plugin is
# available during evaluation. Requires recursive-nix.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-srcfilter-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-srcfilter requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-srcfilter"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      echo "=== srcfilter fixture: pre-filtered src vs srcFilter (doCheck=true) ==="
      # dag.nix asserts at eval time that the srcFilter build's outPath equals
      # the pre-filtered build's and differs from the unfiltered one; `check`
      # only evaluates if both hold.
      check=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/srcfilter/dag.nix -A check \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)
      cat "$check"
      result=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/srcfilter/dag.nix -A viaSrcFilter \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)
      output=$($result/bin/app)
      [ "$output" = "hello srcfilter" ] || { echo "FAIL: unexpected output: '$output'"; exit 1; }

      echo "=== srcfilter fixture: an unreadable directory the predicate rejects ==="
      # The filtered copies must not touch what srcFilter rejects: with a
      # mode-000 directory inside a package directory, evaluation succeeds
      # and yields the derivation of the plain tree. The build user is not
      # root, so the mode is enforced.
      cp -r ${go2nixSrc}/tests/fixtures/srcfilter "$TMPDIR/locked-tree"
      chmod -R u+w "$TMPDIR/locked-tree"
      mkdir -m 000 "$TMPDIR/locked-tree/lib/locked"
      instantiate() {
        GOMODCACHE="$TMPDIR/empty-gmc" nix-instantiate ${go2nixSrc}/tests/fixtures/srcfilter/dag.nix -A viaSrcFilter \
          -I nixpkgs=${nixpkgsPath} \
          --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
          "$@"
      }
      plainDrv=$(instantiate)
      lockedDrv=$(instantiate --arg src "$TMPDIR/locked-tree") || lockedDrv="(evaluation failed)"
      chmod 755 "$TMPDIR/locked-tree/lib/locked"
      [ "$lockedDrv" = "$plainDrv" ] || { echo "FAIL: unreadable lib/locked changed the derivation: $lockedDrv != $plainDrv"; exit 1; }

      echo "PASS: srcfilter" > $out
    ''
