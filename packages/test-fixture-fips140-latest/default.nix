# Regression test for GOFIPS140=latest support.
#
# Builds tests/fixtures/fips140-latest under the dag builder and asserts
# the binary's modinfo and DefaultGODEBUG match a vanilla
# `GOFIPS140=latest go build -trimpath` of the same source. Requires
# recursive-nix.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-fips140-latest-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-fips140-latest requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-fips140-latest"
    {
      nativeBuildInputs = [
        nix
        go
        pkgs.diffutils
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      set -euo pipefail
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"
      export GOCACHE=$TMPDIR/gocache
      export GOPROXY=off
      export GOFLAGS=-mod=mod

      echo "=== Building fips140-latest fixture (dag mode, GOFIPS140=latest) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/fips140-latest/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      echo "=== Runtime output ==="
      output=$($result/bin/fips140-latest)
      echo "$output"
      want="2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
      [ "$output" = "$want" ] || { echo "FAIL: sha256(hello) = $output, want $want"; exit 1; }

      echo "=== modinfo ==="
      go version -m $result/bin/fips140-latest | tee $TMPDIR/g.modinfo
      grep -q "build	GOFIPS140=latest" $TMPDIR/g.modinfo \
        || { echo "FAIL: modinfo missing 'build GOFIPS140=latest'"; exit 1; }
      grep -q "DefaultGODEBUG=.*fips140=on" $TMPDIR/g.modinfo \
        || { echo "FAIL: DefaultGODEBUG missing fips140=on"; exit 1; }

      echo "=== vanilla go build comparison ==="
      work=$TMPDIR/work
      cp -r ${go2nixSrc}/tests/fixtures/fips140-latest $work
      chmod -R u+w $work
      (cd $work && GOFIPS140=latest go build -trimpath -buildvcs=false -o $TMPDIR/vanilla .)

      # Same normalisation as test-golden-vs-gobuild: drop binary-path line,
      # normalise the user-supplied mod version.
      normmodinfo() {
        go version -m "$1" | tail -n +2 \
          | sed -E 's/^(\tmod\t[^\t]+\t)[^\t]+/\1(devel)/'
      }
      normmodinfo $TMPDIR/vanilla > $TMPDIR/v.modinfo
      normmodinfo $result/bin/fips140-latest > $TMPDIR/gn.modinfo
      if ! diff -u $TMPDIR/v.modinfo $TMPDIR/gn.modinfo; then
        echo "FAIL: modinfo differs from vanilla GOFIPS140=latest go build -trimpath"
        exit 1
      fi
      echo "  IDENTICAL"

      echo "PASS: fips140-latest" > $out
    ''
