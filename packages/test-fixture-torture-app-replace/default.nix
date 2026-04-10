# default mode fixture test: lockfile-free + fork-style replace directive.
#
# app-replace's go.mod has `replace go.uber.org/atomic => github.com/uber-go/atomic`,
# so go.sum lists only the replacement path while the modKey nix/dag looks up
# is "go.uber.org/atomic@v1.11.0". Regression for plugin moduleHashes re-keying.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-torture-app-replace-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-torture-app-replace requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    go = pkgs.go_1_26;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "torture-app-replace-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-0kSvZbvcdFZfA/2DrFqbt3zw14K0jrYSgXEBZkjv7Cs=";
      nativeBuildInputs = [
        go
        pkgs.cacert
      ];
      dontUnpack = true;
      buildPhase = ''
        export HOME=$TMPDIR
        export GOMODCACHE=$out
        cd ${go2nixSrc}/tests/fixtures/torture-project/app-replace
        go mod download
      '';
      installPhase = "true";
    };
  in
  pkgs.runCommand "test-dag-fixture-torture-app-replace"
    {
      nativeBuildInputs = [
        nix
        go
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building torture app-replace fixture (lockfile-free, fork replace) ==="
      result=$(GOMODCACHE=${goModules} \
        nix-build ${go2nixSrc}/tests/fixtures/torture-project/dag-app-replace.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      out_val=$($result/bin/app-replace)
      [ "$out_val" = "42" ] || { echo "FAIL: expected 42, got $out_val"; exit 1; }

      echo "=== Asserting modinfo records only modules linked into THIS binary ==="
      # main.go imports go.uber.org/atomic; testify/spew/difflib are
      # transitive test deps of atomic listed in go.sum but never linked.
      go version -m "$result/bin/app-replace" | tee buildinfo.txt
      ndeps=$(grep -cE '^[[:space:]]+dep[[:space:]]' buildinfo.txt || true)
      [ "$ndeps" -eq 1 ] || { echo "FAIL: expected exactly 1 dep line, got $ndeps"; exit 1; }
      grep -E '^[[:space:]]+dep[[:space:]]+go\.uber\.org/atomic[[:space:]]+v1\.11\.0' buildinfo.txt \
        || { echo "FAIL: expected dep go.uber.org/atomic v1.11.0"; exit 1; }
      grep -E '^[[:space:]]+=>[[:space:]]+github\.com/uber-go/atomic[[:space:]]+v1\.11\.0' buildinfo.txt \
        || { echo "FAIL: expected replace => github.com/uber-go/atomic v1.11.0"; exit 1; }

      echo "PASS: torture-app-replace" > $out
    ''
