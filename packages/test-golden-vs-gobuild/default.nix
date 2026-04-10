# Differential golden test: build the same fixture with go2nix
# (buildGoApplication via dag.nix) and with vanilla `go build -trimpath`,
# then compare structurally. Catches any cmd/go divergence regardless of
# whether it was specifically audited.
#
# For each fixture and each binary it produces, asserts:
#   - identical stdout
#   - identical `go version -m` output (path/mod/build/dep lines)
#   - identical `file` classification (ELF type, static/dynamic)
#
# go2nix sets `-buildid ""` for reproducibility while `go build` uses a
# content hash, so binaries are not expected to be byte-identical; the
# checks above are the contract.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-golden-vs-gobuild-unsupported" { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-golden-vs-gobuild requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    inherit (pkgs) lib;
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    # Only testify-basic has third-party deps; reuse the existing FOD so the
    # hash stays in one place.
    testifyGoModules =
      (import ../test-fixture-testify-basic/default.nix { inherit flake pkgs system; }).goModules
        or (pkgs.stdenvNoCC.mkDerivation {
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
        });

    # srcOverlay derivations for cgo-internal-test, mirroring its dag.nix.
    adderOverlay = pkgs.runCommand "adder-overlay" { } ''
      mkdir -p $out
      echo -n "hello-from-overlay" > $out/data.txt
    '';
    stampOverlay = pkgs.runCommand "stamp-overlay" { } ''
      mkdir -p $out
      echo -n "v1.2.3-overlay" > $out/VERSION
    '';

    fixtures = [
      {
        name = "testify-basic";
        bins = [
          {
            rel = ".";
            out = "testify-basic";
          }
        ];
        gomodcache = testifyGoModules;
      }
      {
        name = "lang-loopvar";
        bins = [
          {
            rel = ".";
            out = "lang-loopvar";
          }
        ];
      }
      {
        name = "xtest-local-dep";
        bins = [
          {
            rel = ".";
            out = "xtest-local-dep";
          }
        ];
      }
      {
        name = "cgo-internal-test";
        bins = [
          {
            rel = ".";
            out = "cgo-internal-test";
          }
          {
            rel = "./cmd/purebin";
            out = "purebin";
          }
        ];
        # Applied to the writable source copy before vanilla `go build`
        # so its inputs match what dag.nix's packageOverrides.srcOverlay
        # injects into the per-package compile drv.
        overlays = {
          "internal/adder" = adderOverlay;
          "internal/stamp" = stampOverlay;
        };
      }
    ];

    fixtureScript = f: ''
      echo
      echo "########################################"
      echo "# fixture: ${f.name}"
      echo "########################################"

      ${lib.optionalString (f ? gomodcache) "export GOMODCACHE=${f.gomodcache}"}

      echo "--- go2nix build ---"
      go2nix_out=$(${
        lib.optionalString (f ? gomodcache) "GOMODCACHE=${f.gomodcache} "
      }nix-build ${go2nixSrc}/tests/fixtures/${f.name}/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      echo "--- vanilla go build ---"
      work=$TMPDIR/work-${f.name}
      cp -r ${go2nixSrc}/tests/fixtures/${f.name} $work
      chmod -R u+w $work
      ${lib.concatStringsSep "\n" (
        lib.mapAttrsToList (dir: ov: "cp -rL --no-preserve=mode ${ov}/. $work/${dir}/") (f.overlays or { })
      )}
      ${lib.concatMapStringsSep "\n" (b: ''
        (cd $work && go build -trimpath -buildvcs=false -o $TMPDIR/vanilla-${f.name}-${b.out} ${b.rel})
        compare ${f.name} ${b.out} $go2nix_out/bin/${b.out} $TMPDIR/vanilla-${f.name}-${b.out}
      '') f.bins}
    '';
  in
  pkgs.runCommand "test-golden-vs-gobuild"
    {
      nativeBuildInputs = [
        nix
        go
        pkgs.stdenv.cc
        pkgs.file
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

      # `file` classification, normalised: keep ELF header + linkage; drop
      # interpreter path and BuildID which differ by store path / buildid.
      normfile() {
        file -b "$1" | tr ',' '\n' \
          | grep -E '^ *(ELF|statically linked|dynamically linked)' \
          | sed 's/^ *//'
      }

      # `go version -m` minus the first line (binary path) and with the
      # `mod` line's "(devel)" version normalised — go2nix stamps the
      # buildGoApplication `version` arg there, vanilla emits "(devel)".
      # That arg is user-supplied packaging metadata, not a cmd/go-derived
      # value, so it's excluded from the parity contract.
      normmodinfo() {
        go version -m "$1" | tail -n +2 \
          | sed -E 's/^(\tmod\t[^\t]+\t)[^\t]+/\1(devel)/'
      }

      fail=0
      compare() {
        local fixture=$1 bin=$2 g=$3 v=$4
        echo
        echo "=== compare: $fixture/$bin ==="

        echo "--- stdout ---"
        if ! diff -u <("$v") <("$g"); then
          echo "FAIL: $fixture/$bin stdout differs (vanilla vs go2nix)"; fail=1
        else
          echo "  IDENTICAL"
        fi

        echo "--- go version -m ---"
        normmodinfo "$v" > $TMPDIR/v.modinfo
        normmodinfo "$g" > $TMPDIR/g.modinfo
        if ! diff -u $TMPDIR/v.modinfo $TMPDIR/g.modinfo; then
          echo "FAIL: $fixture/$bin modinfo differs (vanilla vs go2nix)"; fail=1
        else
          cat $TMPDIR/g.modinfo
          echo "  IDENTICAL"
        fi

        echo "--- file ---"
        if ! diff -u <(normfile "$v") <(normfile "$g"); then
          echo "FAIL: $fixture/$bin file classification differs"; fail=1
        else
          normfile "$g"
          echo "  IDENTICAL"
        fi
      }

      ${lib.concatMapStringsSep "\n" fixtureScript fixtures}

      [ "$fail" -eq 0 ] || { echo "FAIL: golden comparison found divergences"; exit 1; }
      echo "PASS: all fixtures match vanilla go build -trimpath" > $out
    ''
