{
  pkgs,
  plugin,
  testFixtures,
}:

let
  goModules = import ./torture-project-gomodcache.nix { inherit pkgs testFixtures; };
in
pkgs.runCommand "go2nix-nix-plugin-eval-test"
  {
    nativeBuildInputs = [
      pkgs.nixVersions.nix_2_34
      pkgs.go
      pkgs.jq
    ];
  }
  ''
    export HOME=$(mktemp -d)
    export NIX_STORE_DIR=$TMPDIR/nix/store
    export NIX_STATE_DIR=$TMPDIR/nix/var
    export NIX_LOG_DIR=$TMPDIR/nix/log
    mkdir -p $NIX_STORE_DIR $NIX_STATE_DIR $NIX_LOG_DIR

    # Populate a writable GOMODCACHE from the download cache FOD.
    export GOMODCACHE=$TMPDIR/gomodcache
    export GOPROXY="file://${goModules}"
    export GONOSUMCHECK='*'
    export GONOSUMDB='*'
    cp -r ${testFixtures}/torture-project $TMPDIR/torture-project
    chmod -R u+w $TMPDIR/torture-project
    cd $TMPDIR/torture-project
    go mod download
    cd /

    result=$(nix-instantiate --eval --strict --json --read-write-mode \
      --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
      --option allow-import-from-derivation false \
      --expr "
        let
          r = builtins.resolveGoPackages {
            src = (toString $TMPDIR/torture-project);
            doCheck = true;
          };
          pkgCount = builtins.length (builtins.attrNames r.packages);
          localPkgCount = builtins.length (builtins.attrNames r.localPackages);
          testPkgCount = builtins.length (builtins.attrNames r.testPackages);
          sample = r.packages.\"\''${builtins.head (builtins.attrNames r.packages)}\";
          localSample = r.localPackages.\"\''${builtins.head (builtins.attrNames r.localPackages)}\";
        in {
          inherit pkgCount localPkgCount testPkgCount;
          modulePath = r.modulePath;
          hasReplMap = builtins.isAttrs r.replacements;
          hasDrvName = builtins.hasAttr \"drvName\" sample;
          hasImports = builtins.hasAttr \"imports\" sample;
          hasModKey = builtins.hasAttr \"modKey\" sample;
          hasSubdir = builtins.hasAttr \"subdir\" sample;
          hasLocalDir = builtins.hasAttr \"dir\" localSample;
          hasLocalImports = builtins.hasAttr \"localImports\" localSample;
          hasThirdPartyImports = builtins.hasAttr \"thirdPartyImports\" localSample;
        }
      ")

    echo "$result" | jq .

    pkgCount=$(echo "$result" | jq -r .pkgCount)
    [ "$pkgCount" -ge 10 ] || { echo "FAIL: expected >= 10 packages, got $pkgCount"; exit 1; }

    localPkgCount=$(echo "$result" | jq -r .localPkgCount)
    [ "$localPkgCount" -ge 1 ] || { echo "FAIL: expected >= 1 local packages, got $localPkgCount"; exit 1; }

    modulePath=$(echo "$result" | jq -r .modulePath)
    [ -n "$modulePath" ] || { echo "FAIL: modulePath is empty"; exit 1; }

    for f in hasReplMap hasDrvName hasImports hasModKey hasSubdir hasLocalDir hasLocalImports hasThirdPartyImports; do
      val=$(echo "$result" | jq -r ".$f")
      [ "$val" = "true" ] || { echo "FAIL: $f = $val"; exit 1; }
    done

    echo "=== No-IFD: derivation-backed input is rejected ==="
    err=$(nix-instantiate --eval --read-write-mode \
      --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
      --option allow-import-from-derivation false \
      --expr '
        builtins.resolveGoPackages {
          go = "''${derivation { name = "bogus-go"; system = builtins.currentSystem; builder = "/no"; }}/bin/go";
          src = ./.;
        }
      ' 2>&1 || true)
    echo "$err"
    case "$err" in
      *"derivation output"*) echo "OK: derivation-output context rejected with clear error" ;;
      *) echo "FAIL: expected 'derivation output' rejection, got: $err"; exit 1 ;;
    esac

    echo "PASS: $pkgCount packages, $localPkgCount local packages, modulePath=$modulePath" > $out
  ''
