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
  # Phase 1: checks only supported for modRoot == "." (single-module case).
  # When modRoot != ".", mainSrc doesn't include local replace targets outside
  # the module root, so test discovery/compilation may fail. Users can override
  # with doCheck = true if their layout doesn't use out-of-tree replaces.
  doCheck ? (modRoot == "."),
  checkFlags ? [ ],
  ...
}@args:

let
  normalizedSubPackages = helpers.normalizeSubPackages subPackages;

  # Build the compile manifest JSON string for a per-package derivation.
  # `deps` is the list of dependency derivations whose importcfg entries
  # need to be merged with stdlib's.
  # Passed as an env var (not builtins.toFile) because the manifest
  # references store paths of other derivations. The shell hook writes
  # it to a file before invoking go2nix.
  mkCompileManifestJSON =
    deps:
    builtins.toJSON {
      version = 1;
      kind = "compile";
      importcfgParts = [ "${stdlib}/importcfg" ] ++ map (dep: "${dep}/importcfg") deps;
      inherit tags;
      gcflags =
        let
          base = gcflags;
        in
        if buildMode == "pie" then [ "-shared" ] ++ base else base;
      pgoProfile = if pgoProfile != null then "${pgoProfile}" else null;
    };

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
    }
    // (if goProxy != null then { inherit goProxy; } else { })
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
      knownOverrideAttrs = [
        "nativeBuildInputs"
        "env"
      ];
      unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = pkgOverride.env or { };

      # Auto-add CC for CGO packages; use stdenvNoCC for pure Go packages.
      isCgo = pkg.isCgo or false;
      cgoBuildInputs = if isCgo then [ stdenv.cc ] else [ ];
      mkDeriv = if isCgo then stdenv.mkDerivation else stdenvNoCC.mkDerivation;
    in
    assert
      unknownAttrs == [ ]
      || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: nativeBuildInputs, env";
    mkDeriv {
      name = pkg.drvName;
      __structuredAttrs = true;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
      buildInputs = deps;

      env =
        goEnv
        // {
          goPackagePath = importPath;
          goPackageSrcDir = srcDir;
          compileManifestJSON = mkCompileManifestJSON deps;
        }
        // extraEnv;
    }
  ) goPackagesResult.packages;

  # --- Local package set (per-package derivations with isolated source) ---
  # Each local package gets its own builtins.path-filtered source containing
  # only its directory. This enables:
  #   - Cross-app sharing: same local package in two apps = same derivation
  #   - Fine-grained rebuilds: changing internal/db doesn't rebuild internal/web
  localPackages = builtins.mapAttrs (
    importPath: pkg:
    let
      # Directory relative to src root, provided by the plugin from go list's Dir field.
      # The plugin guarantees this is "." for root packages or a valid subdirectory path.
      # Empty string is never produced (the plugin throws on resolution failure).
      relDir = pkg.dir;

      # A root-level package (relDir == ".") includes the entire source.
      isRoot = relDir == ".";

      # Per-package filtered source: only this package's directory enters the store.
      pkgSrc = builtins.path {
        path = src;
        name = "golocal-${helpers.sanitizeName importPath}-src";
        filter =
          path: _type:
          if isRoot then
            true
          else
            let
              rel = lib.removePrefix (toString src + "/") (toString path);
            in
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
      mkDeriv = if isCgo then stdenv.mkDerivation else stdenvNoCC.mkDerivation;

      # Per-package overrides (e.g., nativeBuildInputs for cgo).
      pkgOverride = packageOverrides.${importPath} or { };
      knownOverrideAttrs = [
        "nativeBuildInputs"
        "env"
      ];
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
      || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: nativeBuildInputs, env";
    mkDeriv {
      name = "golocal-${helpers.sanitizeName importPath}";
      __structuredAttrs = true;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
      buildInputs = deps;

      env =
        goEnv
        // {
          goPackagePath = importPath;
          goPackageSrcDir = srcDir;
          compileManifestJSON = mkCompileManifestJSON deps;
        }
        // extraEnv;
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
  allPkgsImportcfg =
    let
      thirdPartyEntries = map (
        importPath:
        let
          pkg = packages.${importPath};
        in
        "packagefile ${importPath}=${pkg}/${importPath}.a"
      ) (builtins.attrNames packages);
      localEntries = map (
        importPath:
        let
          pkg = localPackages.${importPath};
        in
        "packagefile ${importPath}=${pkg}/${importPath}.a"
      ) (builtins.attrNames localPackages);
    in
    lib.concatStringsSep "\n" (thirdPartyEntries ++ localEntries);

  depsImportcfg = stdenvNoCC.mkDerivation {
    name = "${pname}-deps-importcfg";
    __structuredAttrs = true;
    inherit allPkgsImportcfg;
    dontUnpack = true;
    dontFixup = true;
    buildPhase = ''
      runHook preBuild
      cat "${stdlib}/importcfg" > "$NIX_BUILD_TOP/importcfg"
      echo "$allPkgsImportcfg" >> "$NIX_BUILD_TOP/importcfg"
      runHook postBuild
    '';
    installPhase = ''
      mkdir -p "$out"
      cp "$NIX_BUILD_TOP/importcfg" "$out/importcfg"
    '';
  };

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
        mkDeriv = if isCgo then stdenv.mkDerivation else stdenvNoCC.mkDerivation;

        pkgOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
        knownOverrideAttrs = [
          "nativeBuildInputs"
          "env"
        ];
        unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
        extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
        extraEnv = pkgOverride.env or { };
      in
      assert
        unknownAttrs == [ ]
        || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: nativeBuildInputs, env";
      mkDeriv {
        name = pkg.drvName;
        __structuredAttrs = true;

        nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
        buildInputs = deps;

        env =
          goEnv
          // {
            goPackagePath = importPath;
            goPackageSrcDir = srcDir;
            compileManifestJSON = mkCompileManifestJSON deps;
          }
          // extraEnv;
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
  ];

  # Test manifest: only materialized when doCheck = true.
  # Selects testDepsImportcfg when test-only deps exist, depsImportcfg otherwise.
  hasTestDeps = doCheck && goPackagesResult ? testPackages && goPackagesResult.testPackages != { };

  linkManifestJSON = builtins.toJSON {
    version = 1;
    kind = "link";
    importcfgParts = [ "${depsImportcfg}/importcfg" ];
    localArchives = builtins.mapAttrs (importPath: pkg: "${pkg}/${importPath}.a") localPackages;
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
  };

  testManifestJSON = lib.optionalString doCheck (
    builtins.toJSON {
      version = 1;
      kind = "test";
      importcfgParts =
        if hasTestDeps then [ "${testDepsImportcfg}/importcfg" ] else [ "${depsImportcfg}/importcfg" ];
      localArchives = builtins.mapAttrs (importPath: pkg: "${pkg}/${importPath}.a") localPackages;
      inherit moduleRoot;
      inherit tags;
      gcflags = if buildMode == "pie" then [ "-shared" ] ++ gcflags else gcflags;
      inherit checkFlags;
    }
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
