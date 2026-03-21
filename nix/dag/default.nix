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
  doCheck ? true,
  checkFlags ? [],
  filterSrc ? false,
  extraSrcPaths ? [],
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

  # --- Optional source filtering (opt-in via filterSrc = true) ---
  # When enabled, only include the modRoot and local replace directories in
  # the store path. Changes to unrelated modules won't trigger rebuilds.
  # The plugin still reads the full src for `go list` (eval-time only);
  # only the build derivation uses the filtered source.
  normalizePath =
    path:
    let
      parts = lib.splitString "/" path;
      resolve =
        acc: part:
        if part == ".." then
          (if acc == [ ] then acc else lib.init acc)
        else if part == "." || part == "" then
          acc
        else
          acc ++ [ part ];
    in
    lib.concatStringsSep "/" (builtins.foldl' resolve [ ] parts);

  effectiveSrc =
    if !filterSrc then
      src
    else
      let
        localDirs = builtins.attrValues goPackagesResult.localReplaces;
        # Resolve replace paths (relative to modRoot) into paths relative to src root.
        resolvedDirs = map (rel: normalizePath "${modRoot}/${rel}") localDirs;
        allowedPrefixes = [ modRoot ] ++ resolvedDirs ++ extraSrcPaths;
      in
      builtins.path {
        path = src;
        name = "${pname}-src";
        filter =
          path: type:
          let
            rel = lib.removePrefix (toString src + "/") (toString path);
          in
          # Allow the root directory itself.
          path == toString src
          # Allow exact matches and children of allowed prefixes.
          || builtins.any (prefix: rel == prefix || lib.hasPrefix (prefix + "/") rel) allowedPrefixes
          # Allow parent directories so Nix descends into them.
          || builtins.any (prefix: lib.hasPrefix (rel + "/") prefix) allowedPrefixes;
      };

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

  thirdPartyDeps = builtins.attrValues packages;

  # --- Importcfg bundle: aggregates all third-party packages into one derivation ---
  # Instead of passing N packages as direct buildInputs to the link derivation,
  # we create a single bundle. This reduces nix-store --realise input validation
  # from O(N) checks to O(1) on rebuilds where only local source changed.
  #
  # Third-party importcfg entries are pre-computed at eval time (we know import
  # paths and store paths from the `packages` attrset). Only stdlib's importcfg
  # needs to be read at build time. Store paths are captured through Nix string
  # context, so they remain derivation dependencies without needing buildInputs.
  thirdPartyImportcfg = lib.concatMapStringsSep "\n" (
    importPath:
    let
      pkg = packages.${importPath};
    in
    "packagefile ${importPath}=${pkg}/${importPath}.a"
  ) (builtins.attrNames packages);

  depsImportcfg = stdenvNoCC.mkDerivation {
    name = "${pname}-deps-importcfg";
    __structuredAttrs = true;
    inherit thirdPartyImportcfg;
    dontUnpack = true;
    dontFixup = true;
    buildPhase = ''
      runHook preBuild
      cat "${stdlib}/importcfg" > "$NIX_BUILD_TOP/importcfg"
      echo "$thirdPartyImportcfg" >> "$NIX_BUILD_TOP/importcfg"
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

  moduleRoot = if modRoot == "." then "${effectiveSrc}" else "${effectiveSrc}/${modRoot}";
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
    "filterSrc"
    "extraSrcPaths"
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
      doCheck
      ;
    src = effectiveSrc;

    nativeBuildInputs = [ hooks.goAppHook ] ++ overrideNativeBuildInputs ++ nativeBuildInputs;
    buildInputs = [ depsImportcfg ];

    disallowedReferences = lib.optional (!allowGoReference) go;

    passthru = {
      inherit
        go
        go2nix
        goLock
        packages
        ;
      inherit (goPackagesResult) localReplaces;
    };

    env = {
      goModuleRoot = moduleRoot;
      goSubPackages = concatStringsSep " " subPackages;
      goLdflags = ldflagsStr;
      goGcflags = gcflagsStr;
      # When filterSrc is active, reference the lockfile from within effectiveSrc
      # to avoid a store dependency on the unfiltered src.
      goLockfile =
        if filterSrc then
          "${effectiveSrc}/${modRoot}/go2nix.toml"
        else
          "${goLock}";
      goPname = pname;
    }
    // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { })
    // (if pgoProfile != null then { goPgoProfile = "${pgoProfile}"; } else { })
    // (if checkFlags != [] then { goCheckFlags = concatStringsSep " " checkFlags; } else { });
  }
)
