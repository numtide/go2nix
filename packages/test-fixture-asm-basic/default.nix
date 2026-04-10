# default mode fixture test: Go-assembly (.s) source.
#
# Exercises compileWithAsm: -gensymabis → compile -symabis -asmhdr →
# asm → pack-append. The bodyless decl in add_amd64.go fails to compile
# if the .s file is dropped, so build success is the regression check.
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-fixture-asm-basic-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-fixture-asm-basic requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;
  in
  pkgs.runCommand "test-dag-fixture-asm-basic"
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

      echo "=== Building asm-basic fixture (default mode, .s source) ==="
      result=$(nix-build ${go2nixSrc}/tests/fixtures/asm-basic/dag.nix \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      got=$($result/bin/asm-basic)
      [ "$got" = "42" ] || { echo "FAIL: got '$got', want 42"; exit 1; }

      echo "=== Asserting binary is statically linked (pure Go + asm, no cgo) ==="
      file $result/bin/asm-basic | tee /dev/stderr | grep -q "statically linked" \
        || { echo "FAIL: asm-basic should be statically linked"; exit 1; }

      echo "PASS: asm-basic" > $out
    ''
