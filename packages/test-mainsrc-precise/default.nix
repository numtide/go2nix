# Eval-level assertions for the file-precise mainSrc filter.
#
# Instantiates tests/fixtures/mainsrc-precise/dag.nix's passthru.mainSrc and
# checks that exactly the files Go would itself read are present (compiled
# sources, *_test.go, resolved //go:embed targets, testdata/) while docs and
# unrelated config are not.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "mainsrc-precise-test-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "mainsrc-precise-test requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "mainsrc-precise-test"
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
        ${go2nixSrc}/tests/fixtures/mainsrc-precise/dag.nix)

      echo "mainSrc = $ms"
      echo "size: $(du -sb "$ms" | cut -f1) bytes"
      echo "files: $(find "$ms" -type f | wc -l)"
      find "$ms" -type f | sed "s|$ms/|  |" | sort

      fail=0
      assert_in()  { if [ -f "$ms/$1" ]; then echo "  ok: IN  $1"; else echo "FAIL: IN  $1 (missing)"; fail=1; fi; }
      assert_out() { if [ ! -e "$ms/$1" ]; then echo "  ok: OUT $1"; else echo "FAIL: OUT $1 (present)"; fail=1; fi; }

      assert_in  go.mod
      assert_in  cmd/app/main.go
      assert_in  internal/greet/greet.go
      assert_in  internal/greet/greet_test.go
      assert_in  internal/greet/testdata/expected.txt
      assert_in  internal/embed/embed.go
      assert_in  internal/embed/embed_test.go
      assert_in  internal/embed/schema.json
      # Test-only //go:embed target NOT under testdata/ — proves the
      # local_test_embed_files merge from the -test pass works.
      assert_in  internal/embed/schema_test.json
      # extraMainSrcFiles entry — runtime-read, not testdata/, not //go:embed
      assert_in  internal/greet/adjacent.conf
      assert_out internal/greet/README.md
      assert_out internal/greet/unrelated.yaml
      assert_out go2nix.toml
      assert_out dag.nix

      [ "$fail" -eq 0 ] || exit 1
      echo "mainsrc-precise-test: 14 assertions passed" > $out
    ''
