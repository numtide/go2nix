# tests/nix/nested_module_test.nix — regression for pkgSrc/mainSrc leaking
# nested-module subtrees.
#
# tests/fixtures/modroot-nested/app/nested-module/ has its own go.mod, so
# go list stops there and its files are never compiled. The mainSrc and
# per-package pkgSrc filters must drop the whole subtree; otherwise touching
# a nested-module file would invalidate the parent package's compile drv.
#
# Plugin-wrapped: mainSrc reads goPackagesResult.nestedModuleRoots, so the
# inner evaluation needs --option plugin-files.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "nested-module-test-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "nested-module-test requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "nested-module-test"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      ms=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-instantiate --eval --read-write-mode --raw \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        -A passthru.mainSrc \
        ${go2nixSrc}/tests/fixtures/modroot-nested/dag.nix)

      fail=0
      check() { if ! eval "$1"; then echo "FAIL: $2"; fail=1; else echo "  ok: $2"; fi; }

      check '[ -e "$ms/app/go.mod" ]'             "modRoot's own go.mod must be present"
      check '[ -e "$ms/app/main.go" ]'            "main.go must be present"
      check '[ -e "$ms/app/internal/util/util.go" ]' "internal/util (a real local package) must be present"
      check '[ ! -e "$ms/app/nested-module" ]'    "nested-module subtree must NOT be in mainSrc"

      # Per-package pkgSrc check: a go.mod-bearing dir under testdata/ is
      # a nested-module boundary the pkgSrc filter must drop (mainSrc
      # includes testdata/ wholesale, so check pkgSrc separately).
      pkgSrc=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-instantiate --eval --read-write-mode --raw \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        -E '(import ${go2nixSrc}/tests/fixtures/modroot-nested/dag.nix).passthru.localPackages."example.com/modroot-nested/internal/util".goPackageSrcDir')
      check '[ -e "$pkgSrc/util.go" ]'            "pkgSrc(util) must include util.go"
      check '[ ! -e "$pkgSrc/testdata/mod" ]'     "pkgSrc(util) must NOT include testdata/mod (nested go.mod boundary)"

      [ "$fail" -eq 0 ] || exit 1
      echo "nested-module-test: 6 assertions passed" > $out
    ''
