# default mode fixture test: a per-build CGO_ENABLED and per-build tags
# reach the standard library, the package compiles, the link and the tests.
#
# Builds tests/fixtures/stdlib-per-build six ways (its variants.nix
# describes them) and inspects each result: how it is linked, what
# `go version -m` records, and which standard-library symbols it contains.
# A setting that stops at the package file lists links the scope's standard
# library, and the binary then says CGO_ENABLED=0 or -tags=netgo while it
# carries the cgo resolver. Then compares the standard-library derivations:
# only the tags the standard library reads may give a build its own, and
# only when the plugin's toolchain is the release of the scope's `go`.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-stdlib-per-build-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-stdlib-per-build requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-stdlib-per-build"
    {
      nativeBuildInputs = [
        nix
        pkgs.file
        pkgs.go
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      source ${./inspect-binary.sh}

      fixture=${go2nixSrc}/tests/fixtures/stdlib-per-build/dag.nix
      nixArgs=(
        -I nixpkgs=${nixpkgsPath}
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so"
      )

      # stdlibOf <variant>: the one go-stdlib derivation in the closure of the
      # application derivation, kept in stdlib-<variant>; leaves closure.txt.
      stdlibOf() {
        drv=$(nix-instantiate "$fixture" "''${nixArgs[@]}" --argstr variant "$1")
        nix-store -qR "$drv" > closure.txt
        grep -E -- '-go-stdlib-[^/]*\.drv$' closure.txt > "stdlib-$1" || true
        cat "stdlib-$1"
        [ "$(wc -l < "stdlib-$1")" = 1 ] || fail "want one go-stdlib derivation in the closure"
      }

      # check <variant> <CGO_ENABLED of the package compiles or ""> <inspectBinary arguments after the binary>
      check() {
        variant=$1
        local pkgCgo=$2

        echo "=== $variant: build, inspect the binary ==="
        result=$(nix-build "$fixture" "''${nixArgs[@]}" --argstr variant "$variant" \
          --arg doCheck false --no-out-link)
        inspectBinary "$result/bin/stdlib-per-build" "''${@:3}"

        echo "=== $variant: one standard library, CGO_ENABLED on the package compiles ==="
        stdlibOf "$variant"
        pkgDrv=$(grep -E -- '-golocal-example\.com-stdlib-per-build-internal-dualcgo\.drv$' closure.txt) \
          || fail "no derivation for internal/dualcgo in the closure"
        nix derivation show "$pkgDrv" > pkg.json
        if [ -z "$pkgCgo" ]; then
          ! grep -q 'CGO_ENABLED' pkg.json || fail "$pkgDrv must not have CGO_ENABLED in its environment"
        else
          grep -q -E "\"CGO_ENABLED\": *\"$pkgCgo\"" pkg.json \
            || fail "$pkgDrv does not have CGO_ENABLED=$pkgCgo in its environment"
        fi

        echo "=== $variant: main_test.go inspects the test binary ==="
        nix-build "$fixture" "''${nixArgs[@]}" --argstr variant "$variant" --no-out-link
      }

      check default "" cgo   "dynamically linked" 1 ""                         yes yes no
      check cgo0    0  nocgo "statically linked"  0 ""                         no  no  no
      check tags    "" cgo   "dynamically linked" 1 netgo,osusergo,timetzdata  no  no  yes
      check cgo1    1  cgo   "dynamically linked" 1 ""                         yes yes no
      check unread  "" cgo   "dynamically linked" 1 unread_by_std              yes yes no
      check netgo   "" cgo   "dynamically linked" 1 netgo                      no  yes no

      echo "=== which builds get a standard library of their own ==="
      variant=unread
      cmp stdlib-unread stdlib-default \
        || fail "a tag the standard library does not read must leave the scope's in place"
      variant=netgo
      ! cmp -s stdlib-netgo stdlib-default || fail "netgo must not get the scope's standard library"
      variant=mixed
      stdlibOf mixed
      cmp stdlib-mixed stdlib-netgo \
        || fail "only the tags the standard library reads may tell two of them apart"
      variant=skew
      stdlibOf skew
      grep -q -- '-patched-tags-' stdlib-skew \
        || fail "what another Go release than the scope's reads must not decide: every tag counts"

      echo "PASS: stdlib-per-build" > $out
    ''
