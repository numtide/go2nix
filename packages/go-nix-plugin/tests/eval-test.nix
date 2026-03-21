{
  pkgs,
  plugin,
  testFixtures,
}:

let
  # Pre-fetch Go modules as a zstd-compressed tarball (single file → fast nix copy).
  goModCacheArchive = pkgs.stdenvNoCC.mkDerivation {
    name = "torture-project-gomodcache.tar.zst";
    src = testFixtures + "/torture-project";
    nativeBuildInputs = [ pkgs.go pkgs.cacert pkgs.zstd ];
    outputHashMode = "flat";
    outputHashAlgo = "sha256";
    outputHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
    phases = [ "buildPhase" ];
    buildPhase = ''
      export HOME=$TMPDIR
      export GOPATH=$TMPDIR/go
      export GOMODCACHE=$TMPDIR/gomodcache
      cd $src
      go mod download
      tar -cf - -C $TMPDIR gomodcache | zstd -19 -o $out
    '';
  };
in
pkgs.runCommand "go2nix-nix-plugin-eval-test"
  {
    nativeBuildInputs = [
      pkgs.nixVersions.nix_2_34
      pkgs.go
      pkgs.jq
      pkgs.zstd
    ];
  }
  ''
    export HOME=$(mktemp -d)
    export NIX_STORE_DIR=$TMPDIR/nix/store
    export NIX_STATE_DIR=$TMPDIR/nix/var
    export NIX_LOG_DIR=$TMPDIR/nix/log
    mkdir -p $NIX_STORE_DIR $NIX_STATE_DIR $NIX_LOG_DIR

    export GOMODCACHE=$TMPDIR/gomodcache
    tar -xf ${goModCacheArchive} -C $TMPDIR --zstd

    cp -r ${testFixtures}/torture-project $TMPDIR/torture-project
    chmod -R u+w $TMPDIR/torture-project

    result=$(nix-instantiate --eval --strict --json --read-write-mode \
      --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
      --expr "
        let
          r = builtins.resolveGoPackages {
            go = \"$(which go)\";
            src = (toString $TMPDIR/torture-project);
          };
          pkgCount = builtins.length (builtins.attrNames r.packages);
          sample = r.packages.\"\''${builtins.head (builtins.attrNames r.packages)}\";
        in {
          inherit pkgCount;
          hasReplMap = builtins.isAttrs r.replacements;
          hasLocalRepl = builtins.isAttrs r.localReplaces;
          hasDrvName = builtins.hasAttr \"drvName\" sample;
          hasImports = builtins.hasAttr \"imports\" sample;
          hasModKey = builtins.hasAttr \"modKey\" sample;
          hasSubdir = builtins.hasAttr \"subdir\" sample;
        }
      ")

    echo "$result" | jq .

    pkgCount=$(echo "$result" | jq -r .pkgCount)
    [ "$pkgCount" -ge 10 ] || { echo "FAIL: expected >= 10 packages, got $pkgCount"; exit 1; }

    for f in hasReplMap hasLocalRepl hasDrvName hasImports hasModKey hasSubdir; do
      val=$(echo "$result" | jq -r ".$f")
      [ "$val" = "true" ] || { echo "FAIL: $f = $val"; exit 1; }
    done

    echo "PASS: $pkgCount packages, all fields present" > $out
  ''
