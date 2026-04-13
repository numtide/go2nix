# Negative integration check: a bad-signature test function must surface as
# a build failure through the testrunner, not be silently dropped.
#
# Copies the test-helper-pkg fixture, appends `func TestBadSig(x int) {}` to
# the in-scope internal/app package, and asserts the inner build fails with
# the upstream `wrong signature for TestBadSig` error from checkTestFunc.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "testmain-badsig-test-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "testmain-badsig requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    dag = pkgs.writeText "dag.nix" ''
      let
        pkgs = import <nixpkgs> { };
        inherit (pkgs) go;
        go2nix = import ${go2nixSrc}/packages/go2nix { inherit pkgs; };
        goEnv = import ${go2nixSrc}/nix/mk-go-env.nix {
          inherit go go2nix;
          inherit (pkgs) callPackage;
        };
      in
      goEnv.buildGoApplication {
        pname = "test-helper-pkg-badsig";
        version = "0.0.1";
        src = ./.;
        goLock = ./go2nix.toml;
        doCheck = true;
      }
    '';
  in
  pkgs.runCommand "testmain-badsig-test"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      cp -r ${go2nixSrc}/tests/fixtures/test-helper-pkg $TMPDIR/fixture
      chmod -R u+w $TMPDIR/fixture
      cp ${dag} $TMPDIR/fixture/dag.nix

      cat >> $TMPDIR/fixture/internal/app/app_test.go <<'EOF'

      func TestBadSig(x int) {}
      EOF

      drv=$(nix-instantiate $TMPDIR/fixture/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so")

      set +e
      nix-build "$drv" --no-out-link
      rc=$?
      set -e

      if [ "$rc" -eq 0 ]; then
        echo "FAIL: build unexpectedly succeeded; bad-signature test function was silently skipped" >&2
        exit 1
      fi
      # Under recursive-nix the inner build's stdout/stderr is written to the
      # daemon's per-drv log, not to the wrapping nix-build's stderr (which only
      # carries "Cannot build …"). Read the recorded log to assert on content.
      nix-store --read-log "$drv" > $TMPDIR/buildlog
      cat $TMPDIR/buildlog
      if ! grep -q "wrong signature for TestBadSig" $TMPDIR/buildlog; then
        echo "FAIL: build failed but log lacks 'wrong signature for TestBadSig'" >&2
        exit 1
      fi

      echo "PASS: build failed with expected checkTestFunc error" > $out
    ''
