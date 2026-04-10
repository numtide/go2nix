# default mode fixture test: cgo with a transitive C++ dependency that links a
# real external library (libsnappy.so) via `#cgo pkg-config:` and
# packageOverrides.<pkg>.nativeBuildInputs.
#
# main.go is pure Go; internal/snap has a .cc shim calling snappy::Compress.
# Exercises packageOverrides → resolvePkgConfig → compileCgo CXXFiles →
# transitive cxx=true → linkbinary -extld $CXX → external linker against
# libsnappy.so. cxx-cgo covers the same -extld step but with an in-tree .cc
# only and no pkg-config / external .so.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-cxx-pkgconfig-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-cxx-pkgconfig requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-cxx-pkgconfig"
    {
      nativeBuildInputs = [
        nix
        pkgs.file
        pkgs.patchelf
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building cxx-pkgconfig fixture (pkg-config + external libsnappy.so) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/cxx-pkgconfig/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link 2>build.log)
      cat build.log >&2

      # The dynimport test-link uses CXX when the package has C++ sources
      # (cmd/go's gccld); a CC-driven attempt would emit
      # "undefined reference to symbol '_ZdlPvm'" before the build recovers.
      ! grep -qE 'undefined reference|_ZdlPvm' build.log \
        || { echo "FAIL: dynimport test-link used CC for a CXXFiles package"; exit 1; }

      got=$($result/bin/cxx-pkgconfig)
      [ "$got" = "hello-snappy" ] || { echo "FAIL: got '$got', want hello-snappy"; exit 1; }

      echo "=== Asserting binary is dynamically linked and references libsnappy.so ==="
      file $result/bin/cxx-pkgconfig | tee /dev/stderr | grep -q "dynamically linked" \
        || { echo "FAIL: cxx-pkgconfig should be dynamically linked"; exit 1; }
      patchelf --print-needed $result/bin/cxx-pkgconfig | tee /dev/stderr | grep -q "^libsnappy" \
        || { echo "FAIL: cxx-pkgconfig should NEEDED libsnappy.so"; exit 1; }

      # cgowork's MkdirTemp suffix used to leak into go tool compile output
      # (the broader TrimPath rewrite left cgo_work_<uid>_<random>/ behind in
      # __.PKGDEF and _go_.o). The cgowork-specific rewrite rule strips it to
      # bare filenames, so the dir name must not appear in any compiled .a.
      echo "=== Asserting cgowork temp dir does not leak into compiled archives ==="
      drv=$(nix-instantiate ${go2nixSrc}/tests/fixtures/cxx-pkgconfig/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" 2>/dev/null)
      for d in $(nix-store -q --references "$drv" | grep -- -golocal-); do
        a=$(find "$(nix-store -q --outputs "$d")" -name '*.a')
        if grep -aq cgo_work_ "$a"; then
          echo "FAIL: $a embeds cgowork temp dir (non-reproducible)"; exit 1
        fi
      done

      echo "PASS: cxx-pkgconfig" > $out
    ''
