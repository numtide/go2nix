# tests/nix/mainsrc_replace_test.nix — regression for mainSrc dropping
# sibling-replace dirs.
#
# When modRoot != "." and go.mod has `replace => ../sibling`, the link
# derivation's mainSrc must include those sibling module roots so the
# testrunner (which re-walks go.mod from ${mainSrc}/${modRoot}) can
# resolve them. torture-project/app-full has 14 such siblings under
# ../internal/*.
#
# Plugin-wrapped: mainSrc reads goPackagesResult.localReplaceDirs, so the
# inner evaluation needs --option plugin-files (and GOMODCACHE for the
# torture project's go list pass).
{
  flake,
  pkgs,
  system,
  ...
}:
if !(flake.packages.${system} ? go2nix-nix-plugin) then
  pkgs.runCommand "mainsrc-replace-test-unsupported" { meta.platforms = pkgs.lib.platforms.linux; } ''
    echo "mainsrc-replace-test requires go2nix-nix-plugin (Linux only)" >&2
    exit 1
  ''
else
  let
    plugin = flake.packages.${system}.go2nix-nix-plugin;
    nix = pkgs.nixVersions.nix_2_34;
    go = pkgs.go_1_26;
    nixpkgsPath = pkgs.path;
    go2nixSrc = flake;

    tortureModules = pkgs.stdenvNoCC.mkDerivation {
      name = "torture-app-full-gomodcache";
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = "sha256-uQKbuVSzWJhqbvPwi1KL5OKlYpPjHoA437m7zgQlrbA=";
      nativeBuildInputs = [
        go
        pkgs.cacert
      ];
      dontUnpack = true;
      buildPhase = ''
        export HOME=$TMPDIR
        export GOMODCACHE=$out
        cd ${go2nixSrc}/tests/fixtures/torture-project/app-full
        go mod download
      '';
      installPhase = "true";
    };
  in
  pkgs.runCommand "mainsrc-replace-test"
    {
      nativeBuildInputs = [ nix ];
      requiredSystemFeatures = [ "recursive-nix" ];
    }
    ''
      export HOME=$(mktemp -d)
      export NIX_CONFIG="extra-experimental-features = nix-command recursive-nix"

      build_mainsrc() {
        GOMODCACHE="${tortureModules}" nix-instantiate --eval --read-write-mode --raw \
          -I nixpkgs=${nixpkgsPath} \
          --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
          -A passthru.mainSrc \
          --argstr doCheck "$1" \
          --expr "
            { doCheck }:
            let pkgs = import <nixpkgs> {}; go = pkgs.go_1_26;
                go2nix = import ${go2nixSrc}/packages/go2nix { inherit pkgs; };
                goEnv = import ${go2nixSrc}/nix/mk-go-env.nix {
                  inherit go go2nix; inherit (pkgs) callPackage;
                };
            in goEnv.buildGoApplication {
              pname = \"mainsrc-replace-test\"; version = \"0.0.1\";
              src = ${go2nixSrc}/tests/fixtures/torture-project;
              goLock = ${go2nixSrc}/tests/fixtures/torture-project/app-full/go2nix.toml;
              modRoot = \"app-full\";
              subPackages = [ \"cmd/app-full\" ];
              doCheck = doCheck == \"true\";
            }"
      }

      ms=$(build_mainsrc true)
      msNoCheck=$(build_mainsrc false)

      fail=0
      check() { if ! eval "$1"; then echo "FAIL: $2"; fail=1; else echo "  ok: $2"; fi; }

      check '[ -e "$ms/app-full/go.mod" ]'        "modRoot's own go.mod must be present"
      check '[ -e "$ms/internal/aws/go.mod" ]'    "sibling replace target ../internal/aws must be in mainSrc"
      check '[ -e "$ms/internal/common/go.mod" ]' "sibling replace target ../internal/common must be in mainSrc"
      check '[ ! -e "$ms/app-partial" ]'          "non-replace sibling app-partial must NOT be in mainSrc"
      check '[ ! -e "$ms/app-replace" ]'          "non-replace sibling app-replace must NOT be in mainSrc"
      check '[ ! -e "$msNoCheck/internal" ]'      "doCheck=false mainSrc must NOT include sibling dirs (no hash churn)"

      [ "$fail" -eq 0 ] || exit 1
      echo "mainsrc-replace-test: 6 assertions passed" > $out
    ''
