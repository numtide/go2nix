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
  buildPackages,
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
  # Defaults to (modRoot == ".") for back-compat: enabling tests changes
  # the link drv hash, and modRoot != "." callers historically built
  # without checks. mainSrc now includes sibling-replace dirs, so passing
  # doCheck = true works for monorepo layouts.
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
    if [ -n "''${goSrcOverlay:-}" ]; then
      cp -rL --no-preserve=mode "$goPackageSrcDir" "$TMPDIR/srcdir"
      cp -rL --no-preserve=mode "$goSrcOverlay"/. "$TMPDIR/srcdir/"
      goPackageSrcDir="$TMPDIR/srcdir"
    fi
    mkdir -p "$out/$(dirname "$goPackagePath")"
    if [ -n "''${iface:-}" ]; then
      mkdir -p "$iface/$(dirname "$goPackagePath")"
      exec "$go2nixBin" \
        compile-package \
        --manifest "$compileManifestJSONPath" \
        --import-path "$goPackagePath" \
        --src-dir "$goPackageSrcDir" \
        --go-version "$goLangVersion" \
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
        --go-version "$goLangVersion" \
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
      # env first so the core wiring below can't be clobbered by a stray
      # builder/args/system/passAsFile key arriving via goEnv or
      # packageOverrides.<pkg>.env. passthrough is an explicit allowlist
      # so it stays last.
      env
      // {
        inherit name;
        # Raw derivations bypass stdenv's platform handling. The builder
        # (bash + go tool compile) runs on the build machine; cross targets
        # are selected via GOOS/GOARCH in `env`, not via drv `system`.
        inherit (stdenv.buildPlatform) system;
        builder = "${buildPackages.bash}/bin/bash";
        args = [ rawGoCompileScript ];
        # compileManifestJSON now carries per-file lists; pass it via a
        # temp file so packages with thousands of source files don't risk
        # MAX_ARG_STRLEN. (The stdenv/cgo path already avoids this via
        # __structuredAttrs.)
        passAsFile = [ "compileManifestJSON" ];
        goPath = "${buildPackages.coreutils}/bin:${go}/bin";
        go2nixBin = "${go2nix}/bin/go2nix";
      }
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
        version = 2;
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
  # base; packageOverrides.<pkg>.env overlays it; the hook-required wiring
  # keys come last so a stray env.compileManifestJSON / goPackagePath /
  # goPackageSrcDir / goLangVersion in an override can't silently break the
  # build (rawGoCompile already protects builder/args/system the same way).
  mkCompileEnv =
    {
      importPath,
      srcDir,
      deps,
      extraEnv,
      srcOverlay ? null,
      files ? null,
      # Module's go-directive (major.minor) for `-lang`. Required for local
      # non-root packages whose filtered srcDir lacks go.mod (so the build-time
      # findGoVersion fallback returns ""). Empty string falls through to
      # findGoVersion in compile-package, which works for third-party modules.
      goVersion ? "",
    }:
    goEnv
    // extraEnv
    // {
      goPackagePath = importPath;
      goPackageSrcDir = srcDir;
      goLangVersion = goVersion;
      compileManifestJSON = mkCompileManifestJSON { inherit deps files; };
    }
    # Only emitted when set so drv hashes for packages without an overlay are
    # unchanged. The "${...}" interpolation carries derivation context so the
    # overlay becomes a build-time input of this compile drv (no IFD).
    // lib.optionalAttrs (srcOverlay != null) { goSrcOverlay = "${srcOverlay}"; };

  # Target platform for `go tool compile`/`link`. goEnv wins so a user-set
  # GOOS/GOARCH (via mkGoEnv { goEnv = ...; }) is honoured everywhere the
  # plugin/link manifest/buildMode look at the target.
  targetGoos = goEnv.GOOS or stdenv.hostPlatform.go.GOOS or null;
  targetGoarch = goEnv.GOARCH or stdenv.hostPlatform.go.GOARCH or null;

  # Match Go's internal/platform.DefaultPIE: PIE for darwin, windows, android, ios.
  buildMode =
    let
      goos = targetGoos;
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
      # `go` is intentionally omitted: the plugin uses the toolchain baked in
      # at its own build time. Passing "${go}/bin/go" would carry derivation
      # context, which the plugin rejects to keep this path IFD-free.
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
      goos = targetGoos;
      goarch = targetGoarch;
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
      inherit fetchPath version;
      dirSuffix = "${helpers.escapeModPath fetchPath}@${helpers.escapeModPath version}";
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
      isExactOverride = builtins.hasAttr importPath packageOverrides;
      rawOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
      # nativeBuildInputs only reaches PATH via stdenv (cgo path);
      # rawGoCompile (non-cgo) hardcodes goPath and discards it. Reject
      # for non-cgo so users get an error instead of a silent no-op —
      # but only for exact-match overrides. A module-path override
      # legitimately spans both cgo and non-cgo packages in the same
      # module, so for the fallback case we silently drop the attr.
      pkgOverride =
        if isExactOverride || isCgo then
          rawOverride
        else
          builtins.removeAttrs rawOverride [ "nativeBuildInputs" ];
      knownOverrideAttrs = [
        "env"
        "srcOverlay"
      ]
      ++ lib.optional isCgo "nativeBuildInputs";
      unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = pkgOverride.env or { };
      srcOverlay = pkgOverride.srcOverlay or null;

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
          srcOverlay
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

  # O(1) "is this rel-dir a local package?" lookup, used to exclude
  # nested packages from a parent's pkgSrc — without this, touching
  # internal/store/ring.go invalidates the parent main package
  # because the recursive filter includes everything under the parent dir.
  # go:embed files in non-package subdirs are still included.
  # builtins.path does not descend into a directory the filter rejects,
  # so an exact-match attrset check is sufficient (no prefix scan).
  localPkgDirSet = builtins.listToAttrs (
    lib.mapAttrsToList (_: p: {
      name = p.dir;
      value = true;
    }) goPackagesResult.localPackages
  );

  # Main module's go directive (major.minor). Threaded explicitly to every
  # local-package compile because non-root pkgSrc filters exclude go.mod,
  # so the build-time go.mod walk would otherwise miss it and compile
  # without -lang (silently flipping loopvar semantics for pre-1.22 modules).
  localGoVersion = goPackagesResult.goVersion or "";

  localPackages = builtins.mapAttrs (
    importPath: pkg:
    let
      # Directory relative to src root, provided by the plugin from go list's Dir field.
      # The plugin guarantees this is "." for root packages or a valid subdirectory path.
      # Empty string is never produced (the plugin throws on resolution failure).
      relDir = pkg.dir;

      # A root-level package (relDir == ".") includes the entire source.
      isRoot = relDir == ".";

      # Per-package filtered source: only this package's directory enters
      # the store. Rooting at src+relDir (not src) means builtins.path
      # walks just this subtree instead of pruning from the top of src
      # for every local package. The filter only needs to drop nested
      # package directories — builtins.path skips a rejected directory's
      # children, so an O(1) set lookup on the directory itself suffices.
      srcStr = toString src;
      pkgSrc = builtins.path {
        path = if isRoot then src else src + "/${relDir}";
        name = "golocal-${helpers.sanitizeName importPath}-src";
        filter =
          path: type:
          let
            rel = lib.removePrefix (srcStr + "/") (toString path);
          in
          # Exclude directories that are themselves other local packages
          # (their sources are isolated in their own pkgSrc). The root
          # itself is never passed to the filter, so relDir won't match.
          !(type == "directory" && localPkgDirSet ? ${rel});
      };

      srcDir = "${pkgSrc}";

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

      # Per-package overrides (e.g., nativeBuildInputs for cgo libraries).
      # Lookup order: exact import path, then module path, then empty.
      isExactOverride = builtins.hasAttr importPath packageOverrides;
      rawOverride =
        packageOverrides.${importPath} or packageOverrides.${goPackagesResult.modulePath} or { };
      # nativeBuildInputs only reaches PATH via stdenv (cgo path);
      # rawGoCompile (non-cgo) hardcodes goPath and discards it. Reject
      # for non-cgo so users get an error instead of a silent no-op —
      # but only for exact-match overrides. A module-path override
      # legitimately spans both cgo and non-cgo packages in the same
      # module, so for the fallback case we silently drop the attr.
      pkgOverride =
        if isExactOverride || isCgo then
          rawOverride
        else
          builtins.removeAttrs rawOverride [ "nativeBuildInputs" ];
      knownOverrideAttrs = [
        "env"
        "srcOverlay"
      ]
      ++ lib.optional isCgo "nativeBuildInputs";
      unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = pkgOverride.env or { };
      srcOverlay = pkgOverride.srcOverlay or null;
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
          srcOverlay
          ;
        files = pkg.files or null;
        goVersion = localGoVersion;
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
      inherit (stdenv.buildPlatform) system;
      builder = "${buildPackages.bash}/bin/bash";
      __structuredAttrs = true;
      PATH = "${buildPackages.coreutils}/bin";
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

        isExactOverride = builtins.hasAttr importPath packageOverrides;
        rawOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
        # nativeBuildInputs only reaches PATH via stdenv (cgo path);
        # rawGoCompile (non-cgo) hardcodes goPath and discards it.
        # Filter (don't throw) when matched via module-path fallback —
        # module-level overrides legitimately span cgo and non-cgo packages.
        pkgOverride =
          if isExactOverride || isCgo then
            rawOverride
          else
            builtins.removeAttrs rawOverride [ "nativeBuildInputs" ];
        knownOverrideAttrs = [
          "env"
          "srcOverlay"
        ]
        ++ lib.optional isCgo "nativeBuildInputs";
        unknownAttrs = builtins.attrNames (builtins.removeAttrs pkgOverride knownOverrideAttrs);
        extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
        extraEnv = pkgOverride.env or { };
        srcOverlay = pkgOverride.srcOverlay or null;
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
            srcOverlay
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

  # Source for the final link derivation. The link step itself only needs
  # the main-package directories plus go.mod, but the testrunner re-walks
  # go.mod from ${mainSrc}/${modRoot}, so any local `replace => ../sibling`
  # target must also be present (otherwise doCheck fails with
  # "local replace target ... does not exist").
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

      # Resolve a/b/../c → a/c in src-relative string space. Kept here
      # (not in helpers.nix) because it needs lib for string ops.
      normalizeRelPath =
        p:
        let
          folded = lib.foldl (
            acc: seg:
            if seg == "" || seg == "." then
              acc
            else if seg == ".." then
              if acc == [ ] || lib.last acc == ".." then acc ++ [ ".." ] else lib.init acc
            else
              acc ++ [ seg ]
          ) [ ] (lib.splitString "/" p);
        in
        if folded == [ ] then "." else lib.concatStringsSep "/" folded;

      # Transitively collect local-replace target dirs, src-relative.
      # Reads go.mod via the src store path so it works whether src is a
      # literal path, a fileset.toSource result, or a builtin fetcher.
      replaceDirsOf =
        startDir:
        map (x: x.key) (
          builtins.genericClosure {
            startSet = [ { key = startDir; } ];
            operator =
              { key, ... }:
              let
                goMod = "${src}/${key}/go.mod";
                rels =
                  if lib.hasPrefix ".." key then
                    # Replace target escaped src — nothing more to read.
                    [ ]
                  else if builtins.pathExists goMod then
                    helpers.parseLocalReplaces (builtins.readFile goMod)
                  else
                    [ ];
              in
              map (r: { key = normalizeRelPath "${key}/${r}"; }) rels;
          }
        );
      # Only when tests run — keeps mainSrc (and so the link drv hash)
      # unchanged for doCheck = false builds.
      replaceDirs = lib.optionals doCheck (
        lib.filter (d: d != cleanModRoot && d != "." && !(lib.hasPrefix ".." d)) (
          replaceDirsOf cleanModRoot
        )
      );

      # Include modRoot for go.mod access plus sibling-replace module roots
      # so the testrunner can walk them.
      allowedDirs = [ cleanModRoot ] ++ subPkgDirs ++ replaceDirs;
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
  # Attrs we set ourselves below (env, buildInputs, passthru,
  # disallowedReferences) are also stripped here and merged explicitly
  # so a caller-supplied value is appended rather than silently dropped
  # by the literal-attrset override.
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
    "buildInputs"
    "env"
    "passthru"
    "disallowedReferences"
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
      version = 2;
      kind = "link";
      importcfgParts = [ "${depsImportcfg}/importcfg" ];
      localArchives = builtins.mapAttrs (importPath: pkg: "${pkg}/${importPath}.a") localPackages;
    }
    // lib.optionalAttrs contentAddressed {
      compileImportcfgParts = [ "${depsImportcfg}/importcfg" ];
      localIfaces = builtins.mapAttrs (importPath: pkg: "${pkg.iface}/${importPath}.x") localPackages;
    }
    // {
      subPackages =
        let
          inherit (goPackagesResult) modulePath;
          spImportPath =
            sp:
            let
              clean = lib.removePrefix "./" sp;
            in
            if sp == "." || clean == "" then modulePath else "${modulePath}/${clean}";
        in
        map (sp: {
          path = sp;
          files = goPackagesResult.localPackages.${spImportPath sp}.files or null;
        }) normalizedSubPackages;
      inherit moduleRoot;
      lockfile =
        if goLock != null then
          "${builtins.path {
            path = goLock;
            name = "go2nix-lockfile";
          }}"
        else
          null;
      # Resolved third-party module set, threaded straight to link-binary
      # so debug.BuildInfo.Deps is populated in lockfile-free mode too
      # (link-binary previously read it from the lockfile only).
      # allModules contains replacement targets as separate keys (so
      # fetchGoModule can find their hash); filter those out so modinfo
      # records `dep <orig> => <repl>` rather than a spurious second dep.
      modules =
        let
          replTargets = lib.mapAttrs' (_: r: {
            name = "${r.path}@${if r.version != "" then r.version else ""}";
            value = true;
          }) goPackagesResult.replacements;
          isReplTarget =
            _modKey: m: replTargets."${m.path}@${m.version}" or false || replTargets."${m.path}@" or false;
        in
        lib.mapAttrsToList (
          modKey: m:
          let
            repl = goPackagesResult.replacements.${modKey} or null;
          in
          {
            inherit (m) path version;
          }
          // lib.optionalAttrs (repl != null) {
            replacePath = repl.path;
            replaceVersion = if repl.version != "" then repl.version else m.version;
          }
        ) (lib.filterAttrs (k: v: !(isReplTarget k v)) allModules);
      inherit pname;
      goos = targetGoos;
      goarch = targetGoarch;
      inherit ldflags;
      inherit tags;
      inherit gcflags;
      pgoProfile = if pgoProfile != null then "${pgoProfile}" else null;
    }
  );

  testManifestJSON = lib.optionalString doCheck (
    builtins.toJSON (
      {
        version = 2;
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
    buildInputs = [
      depsImportcfg
    ]
    ++ lib.optional doCheck testDepsImportcfg
    ++ (args.buildInputs or [ ]);

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

    disallowedReferences = lib.optional (!allowGoReference) go ++ (args.disallowedReferences or [ ]);

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
    }
    // (args.passthru or { });

    env =
      goEnv
      // {
        inherit linkManifestJSON;
      }
      // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { })
      // (if doCheck then { inherit testManifestJSON; } else { })
      // (args.env or { });
  }
)
