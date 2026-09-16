# default mode fixture tests: a third-party package (testify/assert) importing
# a module the main go.mod replaces with a directory
# (`replace github.com/pmezard/go-difflib => ./third_party/go-difflib`).
#   - thirdparty-local-replace: testify/assert is in the build graph
#   - thirdparty-local-replace-testonly: testify/assert is a test-only
#     dependency and the replaced module is reachable only through it
# Both `go build` fine; each compile of testify/assert needs a packagefile
# line for go-difflib/difflib in its importcfg.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-thirdparty-local-replace-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-thirdparty-local-replace requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    # Same module set as testify-basic (the replaced go-difflib is simply
    # never read from the cache), so this is that fixture's FOD.
    goModules = pkgs.stdenvNoCC.mkDerivation {
      name = "testify-basic-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-jfyOzY3bhiTD5GZKF9aIGAYL2Bequp76/LGLc0LFFGQ=";
      nativeBuildInputs = [
        go
        pkgs.cacert
      ];
      dontUnpack = true;
      buildPhase = ''
        export HOME=$TMPDIR
        export GOMODCACHE=$out
        cd ${go2nixSrc}/tests/fixtures/testify-basic
        go mod download
      '';
      installPhase = "true";
    };
  in
  pkgs.runCommand "test-dag-fixture-thirdparty-local-replace"
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

      fail() { echo "FAIL: $*"; exit 1; }
      build() {
        GOMODCACHE=${goModules} \
          nix-build ${go2nixSrc}/tests/fixtures/$1/dag.nix \
          -I nixpkgs=${nixpkgsPath} \
          --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
          --no-out-link
      }

      echo "=== Building thirdparty-local-replace (third-party in the build graph) ==="
      result=$(build thirdparty-local-replace)
      [ "$($result/bin/thirdparty-local-replace)" = "ok" ] || fail "unexpected output"

      echo "=== Asserting modinfo records the replaced module like go build ==="
      go version -m "$result/bin/thirdparty-local-replace" | tee buildinfo.txt
      grep -qP '^\tdep\tgithub\.com/pmezard/go-difflib\tv1\.0\.0$' buildinfo.txt \
        || fail "modinfo missing 'dep github.com/pmezard/go-difflib v1.0.0'"
      grep -qP '^\t=>\t\./third_party/go-difflib\t\(devel\)\t$' buildinfo.txt \
        || fail "modinfo missing '=> ./third_party/go-difflib (devel)'"

      echo "=== Building thirdparty-local-replace-testonly (doCheck=true) ==="
      result=$(build thirdparty-local-replace-testonly)
      [ "$($result/bin/thirdparty-local-replace-testonly)" = "hello world" ] || fail "unexpected output"

      echo "PASS: thirdparty-local-replace" > $out
    ''
