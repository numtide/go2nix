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

  thirdPartyDeps = builtins.attrValues packages;

  # Collect nativeBuildInputs from packageOverrides for link-time availability.
  overrideNativeBuildInputs = builtins.concatLists (
    map (attrs: attrs.nativeBuildInputs or [ ]) (builtins.attrValues packageOverrides)
  );

  moduleRoot = if modRoot == "." then "${src}" else "${src}/${modRoot}";
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
  ];

in
stdenv.mkDerivation (
  extraArgs
  // {
    __structuredAttrs = true;

    inherit
      pname
      version
      src
      meta
      ;

    nativeBuildInputs = [ hooks.goAppHook ] ++ overrideNativeBuildInputs ++ nativeBuildInputs;
    buildInputs = thirdPartyDeps;

    disallowedReferences = lib.optional (!allowGoReference) go;

    passthru = {
      inherit
        go
        go2nix
        goLock
        packages
        ;
    };

    env = {
      goModuleRoot = moduleRoot;
      goSubPackages = concatStringsSep " " subPackages;
      goLdflags = ldflagsStr;
      goGcflags = gcflagsStr;
      goLockfile = "${goLock}";
      goPname = pname;
    }
    // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { })
    // (if pgoProfile != null then { goPgoProfile = "${pgoProfile}"; } else { });
  }
)
