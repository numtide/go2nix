# default mode fixture test: cgo with a transitive C++ dependency.
#
# main.go is pure Go; internal/cxxdep has a .cc file that uses
# std::string. The link must use $CXX for -extld (transitive CXXFiles
# closure walk in nix/dag); using $CC would fail on libstdc++ symbols,
# so build success is the regression check.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-cxx-cgo-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-cxx-cgo requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-cxx-cgo"
    {
      nativeBuildInputs = [
        nix
        pkgs.file
      ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      echo "=== Building cxx-cgo fixture (default mode, transitive .cc dep) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/cxx-cgo/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      got=$($result/bin/cxx-cgo)
      [ "$got" = "hello-cxx" ] || { echo "FAIL: got '$got', want hello-cxx"; exit 1; }

      echo "=== Asserting binary is dynamically linked (cgo) ==="
      file $result/bin/cxx-cgo | tee /dev/stderr | grep -q "dynamically linked" \
        || { echo "FAIL: cxx-cgo should be dynamically linked"; exit 1; }

      echo "PASS: cxx-cgo" > $out
    ''
