# go2nix/nix/dag/default.nix — build a Go binary from source + lockfile (DAG mode).
#
# Usage:
#   goEnv.buildGoApplication {
#     src = ./.;
#     goLock = ./go2nix.toml;
#     pname = "my-app";
#     version = "0.1.0";
#     packageOverrides = {
#       "github.com/foo/bar" = {
#         nativeBuildInputs = [ pkg-config libfoo ];
#       };
#     };
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
  ...
}:

{
  src,
  goLock,
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
  checkFlags ? [],
  ...
}@args:

let
  inherit (builtins) concatStringsSep;

  # --- Module resolution from lockfile (pure-Nix TOML parsing) ---
  lockfile = builtins.fromTOML (builtins.readFile goLock);
  modTable = lockfile.mod or { };

  parseModEntry =
    modKey: hash:
    let
      parsed = builtins.match "(.+)@(.+)" modKey;
    in
    if parsed == null then
      builtins.throw "go2nix lockfile: malformed module key '${modKey}' (expected 'path@version')"
    else
      let
        path = builtins.elemAt parsed 0;
        version = builtins.elemAt parsed 1;
      in
      {
        inherit hash path version;
        fetchPath = path;
        dirSuffix = "${helpers.escapeModPath path}@${version}";
      };

  lockfileModules = builtins.mapAttrs parseModEntry modTable;

  # --- Package graph from plugin (eval-time go list) ---
  # goProxy defaults to "off": reads from the host's GOMODCACHE (populated
  # by `go mod download`). No network access or writes during eval.
  goPackagesResult = builtins.resolveGoPackages (
    {
      go = "${go}/bin/go";
      inherit
        src
        tags
        subPackages
        modRoot
        ;
    }
    // (if goProxy != null then { inherit goProxy; } else { })
  );

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
  ) lockfileModules;

  # --- Third-party package set ---
  moduleInfo = builtins.mapAttrs (
    _: mod:
    let
      src = fetchers.fetchGoModule { inherit (mod) hash fetchPath version; };
    in
    mod // { dir = "${src}/${mod.dirSuffix}"; }
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

      env = {
        goPackagePath = importPath;
        goPackageSrcDir = srcDir;
      }
      // (if gcflagsStr != "" then { goGcflags = gcflagsStr; } else { })
      // (if pgoProfile != null then { goPgoProfile = "${pgoProfile}"; } else { })
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
          path: type:
          if isRoot then true
          else
            let
              rel = lib.removePrefix (toString src + "/") (toString path);
            in
            # Allow the root directory itself.
            path == toString src
            # Allow exact match and children of this package's directory.
            || rel == relDir || lib.hasPrefix (relDir + "/") rel
            # Allow parent directories so Nix descends into them.
            || lib.hasPrefix (rel + "/") relDir;
      };

      srcDir = if isRoot then "${pkgSrc}" else "${pkgSrc}/${relDir}";

      # Dependencies: other local packages + third-party packages.
      deps = map (imp: localPackages.${imp}) pkg.localImports
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
    assert !(builtins.any (c: c == "..") (lib.splitString "/" relDir))
      || builtins.throw "go2nix: local package '${importPath}' has dir '${relDir}' outside source tree";
    assert unknownAttrs == [ ]
      || builtins.throw "packageOverrides.${importPath}: unknown attributes ${builtins.toJSON unknownAttrs}. Valid: nativeBuildInputs, env";
    mkDeriv {
      name = "golocal-${helpers.sanitizeName importPath}";
      __structuredAttrs = true;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
      buildInputs = deps;

      env = {
        goPackagePath = importPath;
        goPackageSrcDir = srcDir;
      }
      // (if gcflagsStr != "" then { goGcflags = gcflagsStr; } else { })
      // (if pgoProfile != null then { goPgoProfile = "${pgoProfile}"; } else { })
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
        let pkg = packages.${importPath}; in
        "packagefile ${importPath}=${pkg}/${importPath}.a"
      ) (builtins.attrNames packages);
      localEntries = map (
        importPath:
        let pkg = localPackages.${importPath}; in
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

  # Collect nativeBuildInputs from packageOverrides for link-time availability.
  overrideNativeBuildInputs = builtins.concatLists (
    map (attrs: attrs.nativeBuildInputs or [ ]) (builtins.attrValues packageOverrides)
  );

  # Source for the final link derivation: only the main package directories.
  mainSrc =
    let
      subPkgDirs = map (sp:
        let clean = lib.removePrefix "./" sp;
        in if modRoot == "." then clean else "${modRoot}/${clean}"
      ) subPackages;
      # Include modRoot for go.mod access.
      allowedDirs = [ modRoot ] ++ subPkgDirs;
      # When modRoot is "." or any subPackage resolves to ".", the entire
      # source tree is needed — no filtering required.
      includeAll = builtins.elem "." allowedDirs;
    in
    builtins.path {
      path = src;
      name = "${pname}-main-src";
      filter =
        path: type:
        if includeAll then true
        else
          let
            rel = lib.removePrefix (toString src + "/") (toString path);
          in
          path == toString src
          || builtins.any (prefix: rel == prefix || lib.hasPrefix (prefix + "/") rel) allowedDirs
          || builtins.any (prefix: lib.hasPrefix (rel + "/") prefix) allowedDirs;
    };

  moduleRoot = if modRoot == "." then "${mainSrc}" else "${mainSrc}/${modRoot}";
  ldflagsStr = concatStringsSep " " ldflags;
  gcflagsStr = concatStringsSep " " gcflags;

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
    buildInputs = [ depsImportcfg ];

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
      inherit (goPackagesResult) localReplaces modulePath;
    };

    env = {
      goModuleRoot = moduleRoot;
      goSubPackages = concatStringsSep " " subPackages;
      goLdflags = ldflagsStr;
      goGcflags = gcflagsStr;
      goLockfile = "${builtins.path { path = goLock; name = "go2nix-lockfile"; }}";
      goPname = pname;
    }
    // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { })
    // (if pgoProfile != null then { goPgoProfile = "${pgoProfile}"; } else { })
    // (if checkFlags != [] then { goCheckFlags = concatStringsSep " " checkFlags; } else { });
  }
  # Local package archives for checkPhase: structured attr becomes a bash
  # associative array mapping import path -> store path of compiled .a file.
  # Only included when doCheck is true to preserve the O(1) input-validation
  # optimization from depsImportcfg — without this guard, every local package
  # store path would be a direct dependency of the final derivation via string
  # context, reintroducing the O(N) fan-in that the bundle was designed to avoid.
  // (if doCheck then {
    goLocalArchives = builtins.mapAttrs (
      importPath: pkg: "${pkg}/${importPath}.a"
    ) localPackages;
  } else { })
)
