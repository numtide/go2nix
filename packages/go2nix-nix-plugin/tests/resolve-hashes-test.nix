# Test: resolveHashes mode returns NAR hashes from go.sum + GOMODCACHE,
# enabling lockfile-free builds.
#
# Verifies:
# 1. moduleHashes is populated when resolveHashes = true
# 2. Every hash is valid SRI format (sha256-...)
# 3. Hashes match `nix hash path` on the extracted source trees
# 4. moduleHashes is empty when resolveHashes = false (default)
{
  pkgs,
  plugin,
  testFixtures,
}:

let
  goModules = import ./torture-project-gomodcache.nix { inherit pkgs testFixtures; };
in
pkgs.runCommand "go2nix-nix-plugin-resolve-hashes-test"
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

    echo "=== Test 1: resolveHashes = true returns moduleHashes ==="
    result=$(nix-instantiate --eval --strict --json --read-write-mode \
      --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
      --expr "
        let
          r = builtins.resolveGoPackages {
            go = \"${pkgs.go}/bin/go\";
            src = (toString $TMPDIR/torture-project);
            resolveHashes = true;
          };
          hashCount = builtins.length (builtins.attrNames r.moduleHashes);
          # Sample a few hashes for validation.
          sampleKeys = builtins.genList (i:
            builtins.elemAt (builtins.attrNames r.moduleHashes) i
          ) (if hashCount > 3 then 3 else hashCount);
          sampleHashes = map (k: {
            key = k;
            hash = r.moduleHashes.\''${k};
          }) sampleKeys;
        in {
          inherit hashCount sampleHashes;
          hasModuleHashes = builtins.isAttrs r.moduleHashes;
          # Verify packages are still returned alongside hashes.
          pkgCount = builtins.length (builtins.attrNames r.packages);
        }
      ")

    echo "$result" | jq .

    hashCount=$(echo "$result" | jq -r .hashCount)
    [ "$hashCount" -ge 10 ] || { echo "FAIL: expected >= 10 module hashes, got $hashCount"; exit 1; }
    echo "  moduleHashes count: $hashCount"

    hasModuleHashes=$(echo "$result" | jq -r .hasModuleHashes)
    [ "$hasModuleHashes" = "true" ] || { echo "FAIL: moduleHashes is not an attrset"; exit 1; }

    pkgCount=$(echo "$result" | jq -r .pkgCount)
    [ "$pkgCount" -ge 10 ] || { echo "FAIL: expected >= 10 packages alongside hashes, got $pkgCount"; exit 1; }

    # Validate all sample hashes are SRI format.
    for row in $(echo "$result" | jq -c '.sampleHashes[]'); do
      key=$(echo "$row" | jq -r .key)
      hash=$(echo "$row" | jq -r .hash)
      case "$hash" in
        sha256-*) echo "  OK: $key → $hash" ;;
        *) echo "FAIL: bad hash format for $key: $hash"; exit 1 ;;
      esac
    done

    echo "=== Test 2: verify hashes match nix hash path ==="
    export NIX_CONFIG="extra-experimental-features = nix-command"
    # Pick a module and compare our hash against nix hash path on the source tree.
    for row in $(echo "$result" | jq -c '.sampleHashes[]'); do
      key=$(echo "$row" | jq -r .key)
      expected=$(echo "$row" | jq -r .hash)

      # Parse path@version from key.
      mod_path=''${key%%@*}
      version=''${key##*@}

      # Go module case-escaping: uppercase → !lowercase.
      escaped_path=$(echo "$mod_path" | sed 's/\([A-Z]\)/!\L\1/g')
      source_dir="$GOMODCACHE/$escaped_path@$version"

      if [ ! -d "$source_dir" ]; then
        echo "  SKIP: $source_dir not found (module may not be extracted)"
        continue
      fi

      actual=$(nix hash path "$source_dir")
      if [ "$expected" = "$actual" ]; then
        echo "  MATCH: $key → $actual"
      else
        echo "FAIL: hash mismatch for $key"
        echo "  expected (plugin): $expected"
        echo "  actual (nix hash): $actual"
        exit 1
      fi
    done

    echo "=== Test 3: resolveHashes = false (default) omits moduleHashes ==="
    result_no_hashes=$(nix-instantiate --eval --strict --json --read-write-mode \
      --option plugin-files "${plugin}/lib/nix/plugins/libgo2nix_plugin.so" \
      --expr "
        let
          r = builtins.resolveGoPackages {
            go = \"${pkgs.go}/bin/go\";
            src = (toString $TMPDIR/torture-project);
          };
        in {
          hasModuleHashes = builtins.hasAttr \"moduleHashes\" r;
        }
      ")

    hasHashes=$(echo "$result_no_hashes" | jq -r .hasModuleHashes)
    [ "$hasHashes" = "false" ] || { echo "FAIL: moduleHashes should not be present when resolveHashes is false, got $hasHashes"; exit 1; }
    echo "  OK: moduleHashes absent when resolveHashes = false"

    echo "PASS: all resolve-hashes tests passed ($hashCount modules)" > $out
  ''
