# default mode fixture test: a filesystem-replaced sibling module with a
# require-line version and a different go directive. Asserts cmd/go parity:
#   - modinfo records the sibling as `dep <path> <version>` + `=> ./sib (devel)`
#   - -trimpath rewrites sibling sources to `<path>@<version>/...` so
#     runtime.Caller / stack traces match `go build -trimpath`
#   - the sibling compile drv carries the sibling's own go directive as -lang
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-sibling-versioned-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-sibling-versioned requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-sibling-versioned"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      mkdir -p "$TMPDIR/empty-gmc"

      echo "=== Building sibling-versioned fixture ==="
      result=$(GOMODCACHE="$TMPDIR/empty-gmc" nix-build ${go2nixSrc}/tests/fixtures/sibling-versioned/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      fail() { echo "FAIL: $*"; exit 1; }

      echo "--- modinfo ---"
      mi=$($result/bin/app modinfo)
      echo "$mi"
      printf '%s' "$mi" | grep -qx $'dep\texample.com/sib\tv0.1.0' \
        || fail "modinfo missing sibling 'dep example.com/sib v0.1.0' line"
      printf '%s' "$mi" | grep -qx $'=>\t./sib\t(devel)\t' \
        || fail "modinfo missing sibling '=> ./sib (devel)' line"

      echo "--- trimpath ---"
      tp=$($result/bin/app trimpath)
      echo "$tp"
      [ "$tp" = "example.com/sib@v0.1.0/util/util.go" ] \
        || fail "trimpath rewrote sibling source to '$tp', want 'example.com/sib@v0.1.0/util/util.go'"

      echo "--- per-package -lang (sibling go 1.22, main go 1.23) ---"
      drvEnv() {
        GOMODCACHE="$TMPDIR/empty-gmc" nix-instantiate --eval --json --strict \
          -I nixpkgs=${nixpkgsPath} \
          --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
          --expr "let p = (import ${go2nixSrc}/tests/fixtures/sibling-versioned/dag.nix).passthru.localPackages.\"$1\"; in { inherit (p) goLangVersion goModulePath goModuleVersion; }"
      }
      sibEnv=$(drvEnv "example.com/sib/util")
      echo "sib: $sibEnv"
      [ "$sibEnv" = '{"goLangVersion":"1.22","goModulePath":"example.com/sib","goModuleVersion":"v0.1.0"}' ] \
        || fail "sibling compile drv env mismatch: $sibEnv"
      mainEnv=$(drvEnv "example.com/sibling-versioned/cmd/app")
      echo "main: $mainEnv"
      [ "$mainEnv" = '{"goLangVersion":"1.23","goModulePath":"example.com/sibling-versioned","goModuleVersion":""}' ] \
        || fail "main-module compile drv env mismatch: $mainEnv"

      echo "PASS: sibling-versioned" > $out
    ''
