# go2nix/nix/dag/default.nix — build a Go binary from source (default mode).
#
# Supports two modes for module hash resolution:
#   1. Lockfile: pass goLock = ./go2nix.toml (hashes from checked-in file)
#   2. Lockfile-free: omit goLock (hashes resolved at eval time from
#      go.sum + GOMODCACHE via the nix plugin, cached on disk by h1: hash)
#
# Usage:
#   # With lockfile:
#   goEnv.buildGoApplication {
#     src = ./.;
#     goLock = ./go2nix.toml;
#     pname = "my-app";
#     version = "0.1.0";
#   }
#
#   # Without lockfile (hashes from go.sum + GOMODCACHE):
#   goEnv.buildGoApplication {
#     src = ./.;
#     pname = "my-app";
#     version = "0.1.0";
#   }
{
  stdenv,
  stdenvNoCC,
  go,
  go2nix,
  lib,
  hooks,
  fetchers,
  helpers,
  stdlib,
  goEnv,
  bash,
  coreutils,
  ...
}:

{
  src,
  goLock ? null,
  pname,
  version,
  subPackages ? [ "." ],
  tags ? [ ],
  ldflags ? [ ],
  gcflags ? [ ],
  CGO_ENABLED ? null,
  goProxy ? null,
  pgoProfile ? null,
  allowGoReference ? false,
  meta ? { },
  nativeBuildInputs ? [ ],
  modRoot ? ".",
  packageOverrides ? { },
  # Build per-package and importcfg derivations as floating CA so that
  # rebuilds producing byte-identical outputs short-circuit downstream
  # recompiles. Requires the ca-derivations experimental feature; the
  # final binary stays input-addressed.
  #
  # Each per-package derivation gets a separate `iface` output containing
  # only the export data (.x file via go tool compile -linkobj).
  # Downstream compiles depend on `iface`, so private-symbol changes
  # which alter the .a but not its export data don't cascade. The two
  # mechanisms are coupled by design — CA without iface only short-
  # circuits comment-only and cross-module-boundary edits, while iface
  # without CA can't cut off anything (the input-addressed .x path
  # changes whenever src does).
  contentAddressed ? false,
  # Phase 1: checks only supported for modRoot == "." (single-module case).
  # When modRoot != ".", mainSrc doesn't include local replace targets outside
  # the module root, so test discovery/compilation may fail. Users can override
  # with doCheck = true if their layout doesn't use out-of-tree replaces.
  doCheck ? (modRoot == "."),
  checkFlags ? [ ],
  ...
}@args:

let
  caAttrs = lib.optionalAttrs contentAddressed {
    __contentAddressed = true;
    outputHashMode = "recursive";
    outputHashAlgo = "sha256";
    # Local-package CA outputs are never in any binary cache (they're a
    # function of the working tree). Without this, nix queries every
    # configured substituter for each CA realisation before building —
    # ~78 HTTPS round-trips to cache.nixos.org ≈ 1.0s on every
    # incremental build. allowSubstitutes = false skips both the
    # path-substitution and the drv-output-substitution goals.
    allowSubstitutes = false;
  };
  ifaceAttrs = lib.optionalAttrs contentAddressed {
    outputs = [
      "out"
      "iface"
    ];
  };
  # caOnly: CA without the iface output — for importcfg bundles.
  # caMk: CA + iface split — for per-package compiles. Third-party packages
  # stay input-addressed (fixed source, never rebuild — CA adds resolution
  # overhead without benefit).
  caMk = mk: attrs: mk (attrs // caAttrs // ifaceAttrs);
  # Non-cgo packages use a raw derivation (no stdenv, no phase machinery)
  # for faster per-package compiles. Cgo packages need cc-wrapper from stdenv.
  pickMk = isCgo: if isCgo then stdenv.mkDerivation else rawGoCompile;

  # Where to find a dep's importcfg fragment for downstream compiles.
  # When CA is on, local packages have it in the iface output (points at
  # the .x file); third-party packages have a single output and the
  # importcfg points at the .a (which contains __.PKGDEF).
  depCompileCfg = dep: "${dep.iface or dep}/importcfg";

  # buildInputs for per-package compiles must reference ONLY the iface
  # output when CA is on — referencing the full derivation pulls in
  # `out` (.a link object) too, which changes whenever the body changes,
  # defeating the iface cutoff. The actual file dependency is already
  # captured via string context in compileManifestJSON; buildInputs here
  # is just for the stdenv input closure.
  depBuildInput = dep: dep.iface or dep;

  # Raw-derivation builder for pure-Go packages: bypasses stdenv
  # entirely (no setup.sh, no phase machinery). The go2nix CLI just
  # needs go + coreutils on PATH and a writable HOME/TMPDIR. For a
  # single rustc-style compile this saves the ~2000-line setup.sh
  # source + 4 no-op phases per derivation.
  #
  # Cgo packages still go through stdenv (they need cc-wrapper's env
  # plumbing); the caMk wrapper picks rawGoCompile vs stdenv*.mkDerivation
  # based on isCgo.
  rawGoCompileScript = builtins.toFile "go2nix-compile.sh" ''
    set -eu
    export PATH="$goPath"
    export NIX_BUILD_TOP="$TMPDIR"
    export HOME="$TMPDIR/home"; mkdir -p "$HOME"
    export GOPROXY=off GOSUMDB=off GONOSUMCHECK='*'
    mkdir -p "$out/$(dirname "$goPackagePath")"
    if [ -n "''${iface:-}" ]; then
      mkdir -p "$iface/$(dirname "$goPackagePath")"
      exec "$go2nixBin" \
        compile-package \
        --manifest "$compileManifestJSONPath" \
        --import-path "$goPackagePath" \
        --src-dir "$goPackageSrcDir" \
        --output "$out/$goPackagePath.a" \
        --iface-output "$iface/$goPackagePath.x" \
        --trim-path "$TMPDIR" \
        --importcfg-output "$iface/importcfg"
    else
      exec "$go2nixBin" \
        compile-package \
        --manifest "$compileManifestJSONPath" \
        --import-path "$goPackagePath" \
        --src-dir "$goPackageSrcDir" \
        --output "$out/$goPackagePath.a" \
        --trim-path "$TMPDIR" \
        --importcfg-output "$out/importcfg"
    fi
  '';
  rawGoCompile =
    attrs@{
      name,
      env,
      ...
    }:
    let
      # Pull through only what builtins.derivation understands. caMk
      # adds __contentAddressed/outputHash*/outputs; everything else
      # (nativeBuildInputs, __structuredAttrs, buildInputs) is stdenv
      # machinery we're bypassing. The dep edges are carried via string
      # context in env.compileManifestJSON.
      passthrough = lib.getAttrs (lib.intersectLists (lib.attrNames attrs) [
        "outputs"
        "__contentAddressed"
        "outputHashMode"
        "outputHashAlgo"
        "allowSubstitutes"
        "preferLocalBuild"
      ]) attrs;
    in
    derivation (
      {
        inherit name;
        inherit (stdenv.hostPlatform) system;
        builder = "${bash}/bin/bash";
        args = [ rawGoCompileScript ];
        # compileManifestJSON now carries per-file lists; pass it via a
        # temp file so packages with thousands of source files don't risk
        # MAX_ARG_STRLEN. (The stdenv/cgo path already avoids this via
        # __structuredAttrs.)
        passAsFile = [ "compileManifestJSON" ];
        goPath = "${coreutils}/bin:${go}/bin";
        go2nixBin = "${go2nix}/bin/go2nix";
      }
      // env
      // passthrough
    );

  normalizedSubPackages = helpers.normalizeSubPackages subPackages;

  # Build the compile manifest JSON string for a per-package derivation.
  # `deps` is the list of dependency derivations whose importcfg entries
  # need to be merged with stdlib's.
  # Passed as an env var (not builtins.toFile) because the manifest
  # references store paths of other derivations. The shell hook writes
  # it to a file before invoking go2nix.
  mkCompileManifestJSON =
    {
      deps,
      files,
    }:
    builtins.toJSON (
      {
        version = 1;
        kind = "compile";
        importcfgParts = [ "${stdlib}/importcfg" ] ++ map depCompileCfg deps;
        inherit tags;
        gcflags =
          let
            base = gcflags;
          in
          if buildMode == "pie" then [ "-shared" ] ++ base else base;
        pgoProfile = if pgoProfile != null then "${pgoProfile}" else null;
      }
      // (if files != null then { inherit files; } else { })
    );

  # Env attrset for per-package compile derivations. goEnv is the scope-level
  # base; the hook-required keys overlay it; packageOverrides.<pkg>.env wins.
  mkCompileEnv =
    {
      importPath,
      srcDir,
      deps,
      extraEnv,
      files ? null,
    }:
    goEnv
    // {
      goPackagePath = importPath;
      goPackageSrcDir = srcDir;
      compileManifestJSON = mkCompileManifestJSON { inherit deps files; };
    }
    // extraEnv;

  # Match Go's internal/platform.DefaultPIE: PIE for darwin, windows, android, ios.
  buildMode =
    let
      goos = stdenv.hostPlatform.go.GOOS;
    in
    if
      builtins.elem goos [
        "darwin"
        "windows"
        "android"
        "ios"
      ]
    then
      "pie"
    else
      "exe";

  # --- Module resolution ---
  #
  # Two paths:
  #   1. Lockfile provided (goLock != null): read hashes from go2nix.toml
  #   2. Lockfile-free (goLock == null): plugin resolves NAR hashes from
  #      go.sum + GOMODCACHE at eval time (resolveHashes = true)

  hasLockfile = goLock != null;

  lockfileModTable =
    if hasLockfile then (builtins.fromTOML (builtins.readFile goLock)).mod or { } else { };

  parseModEntry =
    modKey: hash:
    let
      parsed = builtins.match "(.+)@(.+)" modKey;
    in
    if parsed == null then
      builtins.throw "go2nix: malformed module key '${modKey}' (expected 'path@version')"
    else
      let
        path = builtins.elemAt parsed 0;
        version = builtins.elemAt parsed 1;
      in
      {
        inherit hash path version;
        fetchPath = path;
      };

  lockfileModules = builtins.mapAttrs parseModEntry lockfileModTable;

  # --- Package graph from plugin (eval-time go list) ---
  # goProxy is unset by default — inherits the environment's GOPROXY.
  # When resolveHashes is true, the plugin also computes NAR hashes for all
  # modules from go.sum + GOMODCACHE, returned as moduleHashes.
  goPackagesResult = builtins.resolveGoPackages (
    {
      go = "${go}/bin/go";
      inherit
        src
        tags
        modRoot
        doCheck
        ;
      subPackages = normalizedSubPackages;
      resolveHashes = !hasLockfile;
      # File lists in the manifest are computed at eval time by `go list`;
      # pass the same target platform here that the build derivations will
      # use so constraint evaluation matches.
      goos = stdenv.hostPlatform.go.GOOS or null;
      goarch = stdenv.hostPlatform.go.GOARCH or null;
    }
    // (if goProxy != null then { inherit goProxy; } else { })
    // (if CGO_ENABLED != null then { cgoEnabled = toString CGO_ENABLED; } else { })
  );

  # Module hashes from plugin (lockfile-free path).
  pluginModules =
    if hasLockfile then
      { }
    else
      builtins.mapAttrs (modKey: hash: parseModEntry modKey hash) (goPackagesResult.moduleHashes or { });

  # Merge: lockfile modules take precedence, plugin modules fill in the rest.
  allModules = pluginModules // lockfileModules;

  # --- Join: apply replace directives to module fetchPaths ---
  resolvedModules = builtins.mapAttrs (
    modKey: mod:
    let
      repl = goPackagesResult.replacements.${modKey} or null;
      fetchPath = if repl != null then repl.path else mod.path;
      version = if repl != null && repl.version != "" then repl.version else mod.version;
    in
    mod
    // {
      inherit fetchPath;
      dirSuffix = "${helpers.escapeModPath fetchPath}@${version}";
    }
  ) allModules;

  # --- Third-party package set ---
  # sourceOnly: lockfile-free hashes cover the extracted source tree only;
  #             lockfile hashes cover the full GOMODCACHE output.
  moduleInfo = builtins.mapAttrs (
    _: mod:
    let
      src = fetchers.fetchGoModule {
        inherit (mod) hash fetchPath version;
        sourceOnly = !hasLockfile;
      };
    in
    mod
    // {
      dir = if hasLockfile then "${src}/${mod.dirSuffix}" else "${src}";
    }
  ) resolvedModules;

  packages = builtins.mapAttrs (
    importPath: pkg:
    let
      minfo = moduleInfo.${pkg.modKey};
      srcDir = if pkg.subdir == "" then minfo.dir else "${minfo.dir}/${pkg.subdir}";

      # Direct dependency derivations (resolved lazily via Nix's laziness).
      deps = map (imp: packages.${imp}) pkg.imports;

      # Per-package overrides (e.g., nativeBuildInputs for cgo libraries).
      # Lookup order: exact import path, then module path, then empty.
      pkgOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
      # nativeBuildInputs only reaches PATH via stdenv (cgo path);
      # rawGoCompile (non-cgo) hardcodes goPath and discards it. Reject
      # for non-cgo so users get an error instead of a silent no-op.
      knownOverrideAttrs = [ "env" ] ++ lib.optional isCgo "nativeBuildInputs";
      unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = pkgOverride.env or { };

      # Auto-add CC for CGO packages; use stdenvNoCC for pure Go packages.
      isCgo = pkg.isCgo or false;
      cgoBuildInputs = if isCgo then [ stdenv.cc ] else [ ];
      mkDeriv = pickMk isCgo;
    in
    assert
      unknownAttrs == [ ]
      || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: ${lib.concatStringsSep ", " knownOverrideAttrs}${
        lib.optionalString (!isCgo) " (nativeBuildInputs is cgo-only — rawGoCompile hardcodes PATH)"
      }";
    mkDeriv {
      name = pkg.drvName;
      __structuredAttrs = true;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
      buildInputs = map depBuildInput deps;

      env = mkCompileEnv {
        inherit
          importPath
          srcDir
          deps
          extraEnv
          ;
        files = pkg.files or null;
      };
    }
  ) goPackagesResult.packages;

  # --- Local package set (per-package derivations with isolated source) ---
  # Each local package gets its own builtins.path-filtered source containing
  # only its directory. This enables:
  #   - Cross-app sharing: same local package in two apps = same derivation
  #   - Fine-grained rebuilds: changing internal/db doesn't rebuild internal/web

  # Map relDir → set of immediate-child package dirs. Used to exclude
  # nested packages from a parent's pkgSrc — without this, touching
  # internal/store/ring.go invalidates the parent main package
  # because the recursive filter includes everything under the parent dir.
  # go:embed files in non-package subdirs are still included.
  allLocalDirs = lib.mapAttrsToList (_: p: p.dir) goPackagesResult.localPackages;
  childPkgDirsOf =
    relDir:
    let
      prefix = if relDir == "." then "" else relDir + "/";
    in
    builtins.filter (d: d != relDir && (relDir == "." || lib.hasPrefix prefix d)) allLocalDirs;

  localPackages = builtins.mapAttrs (
    importPath: pkg:
    let
      # Directory relative to src root, provided by the plugin from go list's Dir field.
      # The plugin guarantees this is "." for root packages or a valid subdirectory path.
      # Empty string is never produced (the plugin throws on resolution failure).
      relDir = pkg.dir;

      # A root-level package (relDir == ".") includes the entire source.
      isRoot = relDir == ".";

      # Subdirectories under this package that are themselves packages —
      # excluded so their source changes don't invalidate this one.
      nestedPkgDirs = childPkgDirsOf relDir;
      isInNestedPkg = rel: builtins.any (d: rel == d || lib.hasPrefix (d + "/") rel) nestedPkgDirs;

      # Per-package filtered source: only this package's directory enters the store.
      pkgSrc = builtins.path {
        path = src;
        name = "golocal-${helpers.sanitizeName importPath}-src";
        filter =
          path: _type:
          let
            rel = lib.removePrefix (toString src + "/") (toString path);
          in
          if isInNestedPkg rel then
            false
          else if isRoot then
            true
          else
            # Allow the root directory itself.
            path == toString src
            # Allow exact match and children of this package's directory.
            || rel == relDir
            || lib.hasPrefix (relDir + "/") rel
            # Allow parent directories so Nix descends into them.
            || lib.hasPrefix (rel + "/") relDir;
      };

      srcDir = if isRoot then "${pkgSrc}" else "${pkgSrc}/${relDir}";

      # Dependencies: other local packages + third-party packages.
      deps =
        map (imp: localPackages.${imp}) pkg.localImports
        ++ map (imp: packages.${imp}) pkg.thirdPartyImports;

      # CGO handling: same pattern as third-party packages.
      isCgo = pkg.isCgo or false;
      cgoBuildInputs = if isCgo then [ stdenv.cc ] else [ ];
      # Local packages get CA + iface — they're the ones that rebuild
      # on source touches and benefit from early-cutoff.
      mkDeriv = caMk (pickMk isCgo);

      # Per-package overrides (e.g., nativeBuildInputs for cgo).
      pkgOverride = packageOverrides.${importPath} or { };
      # nativeBuildInputs only reaches PATH via stdenv (cgo path);
      # rawGoCompile (non-cgo) hardcodes goPath and discards it.
      knownOverrideAttrs = [ "env" ] ++ lib.optional isCgo "nativeBuildInputs";
      unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = pkgOverride.env or { };
    in
    # Safety (defense-in-depth): reject paths with ".." path components.
    # The plugin already validates via canonical()/relative(), but guard here too.
    assert
      !(builtins.any (c: c == "..") (lib.splitString "/" relDir))
      || builtins.throw "go2nix: local package '${importPath}' has dir '${relDir}' outside source tree";
    assert
      unknownAttrs == [ ]
      || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: ${lib.concatStringsSep ", " knownOverrideAttrs}${
        lib.optionalString (!isCgo) " (nativeBuildInputs is cgo-only — rawGoCompile hardcodes PATH)"
      }";
    mkDeriv {
      name = "golocal-${helpers.sanitizeName importPath}";
      __structuredAttrs = true;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
      buildInputs = map depBuildInput deps;

      env = mkCompileEnv {
        inherit
          importPath
          srcDir
          deps
          extraEnv
          ;
        files = pkg.files or null;
      };
    }
  ) goPackagesResult.localPackages;

  # --- Importcfg bundle: aggregates all packages into one derivation ---
  # Instead of passing N packages as direct buildInputs to the link derivation,
  # we create a single bundle. This reduces nix-store --realise input validation
  # from O(N) checks to O(1) on rebuilds where only local source changed.
  #
  # All importcfg entries are pre-computed at eval time (we know import paths
  # and store paths from the `packages` and `localPackages` attrsets). Only
  # stdlib's importcfg needs to be read at build time. Store paths are captured
  # through Nix string context, so they remain derivation dependencies without
  # needing buildInputs.
  # Third-party only — local entries are passed via linkManifestJSON's
  # localArchives/localIfaces and appended by link-binary, so
  # depsImportcfg doesn't need them. Excluding locals here means
  # depsImportcfg's content is stable across local-source touches
  # (third-party packages don't change), so it CA-cuts-off and the
  # private-touch cascade drops the importcfg rebuild.
  mkAllPkgsCfg =
    pick:
    lib.concatStringsSep "\n" (
      map (importPath: "packagefile ${importPath}=${pick packages.${importPath} importPath}") (
        builtins.attrNames packages
      )
    );

  # Third-party only → single-output .a (contains __.PKGDEF). Local
  # packages flow via linkManifestJSON.localArchives.
  allPkgsImportcfg = mkAllPkgsCfg (pkg: ip: "${pkg}/${ip}.a");

  # Raw-derivation importcfg bundle: just cat + printf, no stdenv.
  # __structuredAttrs is required — allPkgsImportcfg is ~130KB for
  # ~900 packages, over the per-env-var limit. The script sources
  # $NIX_ATTRS_SH_FILE to read it as a bash variable.
  rawDepsImportcfg = derivation (
    {
      name = "${pname}-deps-importcfg";
      inherit (stdenv.hostPlatform) system;
      builder = "${bash}/bin/bash";
      __structuredAttrs = true;
      PATH = "${coreutils}/bin";
      inherit allPkgsImportcfg;
      stdlibCfg = "${stdlib}/importcfg";
      args = [
        (builtins.toFile "deps-importcfg.sh" ''
          set -eu
          . "$NIX_ATTRS_SH_FILE"
          out="''${outputs[out]}"
          mkdir -p "$out"
          cat "$stdlibCfg" > "$out/importcfg"
          printf '%s\n' "$allPkgsImportcfg" >> "$out/importcfg"
        '')
      ];
    }
    // caAttrs
  );

  depsImportcfg = rawDepsImportcfg;

  # --- Test-only third-party package set (only when doCheck = true) ---
  # These are packages reachable only via test imports, discovered by the
  # plugin's second `go list -deps -test` pass. Built with the same pipeline
  # as normal third-party packages. Their dependencies may include packages
  # from the normal `packages` set.
  testPackages = lib.optionalAttrs doCheck (
    builtins.mapAttrs (
      importPath: pkg:
      let
        minfo = moduleInfo.${pkg.modKey};
        srcDir = if pkg.subdir == "" then minfo.dir else "${minfo.dir}/${pkg.subdir}";

        # Dependencies: may reference both normal and test-only third-party packages.
        deps = map (
          imp: if builtins.hasAttr imp packages then packages.${imp} else testPackages.${imp}
        ) pkg.imports;

        isCgo = pkg.isCgo or false;
        cgoBuildInputs = if isCgo then [ stdenv.cc ] else [ ];
        mkDeriv = pickMk isCgo;

        pkgOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
        # nativeBuildInputs only reaches PATH via stdenv (cgo path);
        # rawGoCompile (non-cgo) hardcodes goPath and discards it.
        knownOverrideAttrs = [ "env" ] ++ lib.optional isCgo "nativeBuildInputs";
        unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
        extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
        extraEnv = pkgOverride.env or { };
      in
      assert
        unknownAttrs == [ ]
        || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: ${lib.concatStringsSep ", " knownOverrideAttrs}${
          lib.optionalString (!isCgo) " (nativeBuildInputs is cgo-only — rawGoCompile hardcodes PATH)"
        }";
      mkDeriv {
        name = pkg.drvName;
        __structuredAttrs = true;

        nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
        buildInputs = map depBuildInput deps;

        env = mkCompileEnv {
          inherit
            importPath
            srcDir
            deps
            extraEnv
            ;
          files = pkg.files or null;
        };
      }
    ) goPackagesResult.testPackages
  );

  # --- Test importcfg bundle (only when doCheck = true) ---
  # Superset of depsImportcfg: includes stdlib + all build third-party + local
  # packages + test-only third-party packages. The test runner uses this instead
  # of the build importcfg so that test-only imports (e.g., testify) resolve.
  # Bundled as a single derivation to preserve O(1) input validation on the
  # final app derivation (same pattern as depsImportcfg).
  testDepsImportcfg = lib.optionalAttrs doCheck (
    let
      testOnlyEntries = map (
        importPath:
        let
          pkg = testPackages.${importPath};
        in
        "packagefile ${importPath}=${pkg}/${importPath}.a"
      ) (builtins.attrNames testPackages);
      testOnlyImportcfg = lib.concatStringsSep "\n" testOnlyEntries;
    in
    stdenvNoCC.mkDerivation {
      name = "${pname}-test-deps-importcfg";
      __structuredAttrs = true;
      inherit testOnlyImportcfg;
      dontUnpack = true;
      dontFixup = true;
      buildPhase = ''
        runHook preBuild
        cat "${depsImportcfg}/importcfg" > "$NIX_BUILD_TOP/importcfg"
        if [ -n "$testOnlyImportcfg" ]; then
          echo "$testOnlyImportcfg" >> "$NIX_BUILD_TOP/importcfg"
        fi
        runHook postBuild
      '';
      installPhase = ''
        mkdir -p "$out"
        cp "$NIX_BUILD_TOP/importcfg" "$out/importcfg"
      '';
    }
  );

  # Collect nativeBuildInputs from packageOverrides for link-time availability.
  overrideNativeBuildInputs = builtins.concatLists (
    map (attrs: attrs.nativeBuildInputs or [ ]) (builtins.attrValues packageOverrides)
  );

  # Source for the final link derivation: only the main package directories.
  mainSrc =
    let
      cleanModRoot = lib.removePrefix "./" modRoot;
      subPkgDirs = map (
        sp:
        let
          clean = lib.removePrefix "./" sp;
        in
        if modRoot == "." then clean else "${cleanModRoot}/${clean}"
      ) normalizedSubPackages;
      # Include modRoot for go.mod access.
      allowedDirs = [ cleanModRoot ] ++ subPkgDirs;
      # When modRoot is "." or any subPackage resolves to ".", the entire
      # source tree is needed — no filtering required.
      includeAll = builtins.elem "." allowedDirs;
    in
    builtins.path {
      path = src;
      name = "${pname}-main-src";
      filter =
        path: _type:
        if includeAll then
          true
        else
          let
            rel = lib.removePrefix (toString src + "/") (toString path);
          in
          path == toString src
          || builtins.any (prefix: rel == prefix || lib.hasPrefix (prefix + "/") rel) allowedDirs
          || builtins.any (prefix: lib.hasPrefix (rel + "/") prefix) allowedDirs;
    };

  moduleRoot = if modRoot == "." then "${mainSrc}" else "${mainSrc}/${modRoot}";

  # Filter out known args so extra attrs pass through to mkDerivation.
  extraArgs = builtins.removeAttrs args [
    "src"
    "goLock"
    "pname"
    "version"
    "subPackages"
    "tags"
    "ldflags"
    "gcflags"
    "CGO_ENABLED"
    "goProxy"
    "pgoProfile"
    "allowGoReference"
    "meta"
    "nativeBuildInputs"
    "modRoot"
    "packageOverrides"
    "doCheck"
    "checkFlags"
    "contentAddressed"
  ];

  # Test manifest: only materialized when doCheck = true.
  # Selects testDepsImportcfg when test-only deps exist, depsImportcfg otherwise.
  hasTestDeps = doCheck && goPackagesResult ? testPackages && goPackagesResult.testPackages != { };

  linkManifestJSON = builtins.toJSON (
    {
      version = 1;
      kind = "link";
      importcfgParts = [ "${depsImportcfg}/importcfg" ];
      localArchives = builtins.mapAttrs (importPath: pkg: "${pkg}/${importPath}.a") localPackages;
    }
    // lib.optionalAttrs contentAddressed {
      compileImportcfgParts = [ "${depsImportcfg}/importcfg" ];
      localIfaces = builtins.mapAttrs (importPath: pkg: "${pkg.iface}/${importPath}.x") localPackages;
    }
    // {
      subPackages = normalizedSubPackages;
      inherit moduleRoot;
      lockfile =
        if goLock != null then
          "${builtins.path {
            path = goLock;
            name = "go2nix-lockfile";
          }}"
        else
          null;
      inherit pname;
      goos = stdenv.hostPlatform.go.GOOS or null;
      goarch = stdenv.hostPlatform.go.GOARCH or null;
      inherit ldflags;
      inherit tags;
      inherit gcflags;
      pgoProfile = if pgoProfile != null then "${pgoProfile}" else null;
    }
  );

  testManifestJSON = lib.optionalString doCheck (
    builtins.toJSON (
      {
        version = 1;
        kind = "test";
        importcfgParts =
          if hasTestDeps then [ "${testDepsImportcfg}/importcfg" ] else [ "${depsImportcfg}/importcfg" ];
        localArchives = builtins.mapAttrs (importPath: pkg: "${pkg}/${importPath}.a") localPackages;
      }
      // lib.optionalAttrs contentAddressed {
        compileImportcfgParts =
          if hasTestDeps then [ "${testDepsImportcfg}/importcfg" ] else [ "${depsImportcfg}/importcfg" ];
        localIfaces = builtins.mapAttrs (importPath: pkg: "${pkg.iface}/${importPath}.x") localPackages;
      }
      // {
        inherit moduleRoot;
        inherit tags;
        gcflags = if buildMode == "pie" then [ "-shared" ] ++ gcflags else gcflags;
        inherit checkFlags;
      }
    )
  );

in
stdenv.mkDerivation (
  extraArgs
  // {
    __structuredAttrs = true;

    inherit
      pname
      version
      meta
      ;
    src = mainSrc;
    inherit doCheck;

    nativeBuildInputs = [ hooks.goAppHook ] ++ overrideNativeBuildInputs ++ nativeBuildInputs;
    buildInputs = [ depsImportcfg ] ++ lib.optional doCheck testDepsImportcfg;

    # Skip all the no-op phases. The hook sets configurePhase /
    # buildPhase / installPhase / checkPhase explicitly; the rest are
    # stdenv defaults that do nothing useful for a Go link:
    #   patchPhase, updateAutotoolsGnuConfigScriptsPhase — no autotools
    #   patchELF — no RPATH/RUNPATH (Go sets interpreter directly)
    #   auditTmpdir — -trimpath strips /build/ refs
    # Strip stays — it actually does work and is fast.
    dontUnpack = true;
    dontPatch = true;
    dontPatchELF = true;
    noAuditTmpdir = true;

    disallowedReferences = lib.optional (!allowGoReference) go;

    passthru = {
      inherit
        go
        go2nix
        goLock
        packages
        localPackages
        depsImportcfg
        mainSrc
        ;
      inherit (goPackagesResult) modulePath;
    }
    // lib.optionalAttrs doCheck {
      inherit testPackages testDepsImportcfg;
    };

    env =
      goEnv
      // {
        inherit linkManifestJSON;
      }
      // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { })
      // (if doCheck then { inherit testManifestJSON; } else { });
  }
)
