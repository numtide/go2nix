# Shared test runner for go2nix dag-mode build tests.
#
# Populates GOMODCACHE from a proxyVendor download cache, runs nix-build
# on the specified dag nix file, and runs a check command on the result.
{
  flake,
  pkgs,
  system,
  # Test identity
  testName,
  # Source to build
  src,
  vendorHash,
  # Which dag.nix to evaluate
  dagFile,
  # Shell command to verify the build output; $result is the store path.
  checkCommand ? ''$result/bin/${testName} --help'',
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "test-dag-package-${testName}-unsupported"
    { meta.platforms = pkgs.lib.platforms.linux; }
    ''
      echo "test-dag-package-${testName} requires go2nix-nix-plugin (Linux only)" >&2
      exit 1
    ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    inherit (pkgs) go;

    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    goModules = (pkgs.buildGoModule {
      pname = "${testName}-modules";
      version = "0-test";
      inherit src vendorHash;
      proxyVendor = true;
    }).goModules;
  in
  pkgs.runCommand "test-dag-package-${testName}"
    {
      nativeBuildInputs = [ nix go ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      export GOMODCACHE=$TMPDIR/gomodcache
      export GOPROXY="file://${goModules}"
      export GONOSUMCHECK='*'
      export GONOSUMDB='*'
      cd ${src}
      go mod download

      echo "=== Building ${testName} ==="
      result=$(nix-build ${dagFile} \
        -I nixpkgs=${nixpkgsPath} \
        --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
        --no-out-link)

      ${checkCommand}
      echo "PASS: ${testName}" > $out
    ''
