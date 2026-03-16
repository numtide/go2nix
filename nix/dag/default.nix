# go2nix/nix/build-go-application.nix — build a Go binary from source + lockfile.
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
  pgoProfile ? null,
  allowGoReference ? false,
  meta ? { },
  nativeBuildInputs ? [ ],
  moduleDir ? ".",
  packageOverrides ? { },
  ...
}@args:

let
  inherit (builtins) concatStringsSep;

  # --- Module resolution from lockfile ---
  resolved = builtins.resolveGoModules {
    lock = builtins.readFile goLock;
  };

  # --- Package graph from plugin (eval-time go list) ---
  goPackagesResult = builtins.resolveGoPackages {
    go = "${go}/bin/go";
    inherit src tags subPackages moduleDir;
  };

  # --- Join: apply replace directives to module fetchPaths ---
  modules = builtins.mapAttrs (
    modKey: mod:
    let
      fetchPath = goPackagesResult.replacements.${modKey} or mod.path;
    in
    mod
    // {
      inherit fetchPath;
      dirSuffix = "${helpers.escapeModPath fetchPath}@${mod.version}";
    }
  ) resolved.modules;

  # --- Third-party package set ---
  fetchModule = fetchers.fetchGoModule;

  moduleInfo = builtins.mapAttrs (
    modKey: mod:
    let
      src = fetchModule modKey { inherit (mod) hash fetchPath; };
    in
    mod // { dir = "${src}/${mod.dirSuffix}"; }
  ) modules;

  packages = builtins.mapAttrs (
    importPath: pkg:
    let
      minfo = moduleInfo.${pkg.modKey};
      srcDir = if pkg.subdir == "" then minfo.dir else "${minfo.dir}/${pkg.subdir}";

      # Direct dependency derivations (resolved lazily via Nix's laziness).
      deps = map (imp: packages.${imp}) pkg.imports;

      # Per-package overrides (e.g., nativeBuildInputs for cgo libraries).
      pkgOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = builtins.removeAttrs pkgOverride [ "nativeBuildInputs" ];

      # Auto-add CC for CGO packages.
      cgoBuildInputs = if pkg.isCgo or false then [ stdenv.cc ] else [ ];
    in
    stdenv.mkDerivation {
      name = pkg.drvName;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ cgoBuildInputs ++ extraNativeBuildInputs;
      buildInputs = deps;

      env = {
        goPackagePath = importPath;
        goPackageSrcDir = srcDir;
      }
      // (if pgoProfile != null then { goPgoProfile = "${pgoProfile}"; } else { })
      // extraEnv;
    }
  ) goPackagesResult.packages;

  require = builtins.attrValues packages;

  # Collect nativeBuildInputs from packageOverrides for link-time availability.
  overrideNativeBuildInputs = builtins.concatLists (
    map (attrs: attrs.nativeBuildInputs or [ ]) (builtins.attrValues packageOverrides)
  );

  moduleRoot = if moduleDir == "." then "${src}" else "${src}/${moduleDir}";
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
    "pgoProfile"
    "allowGoReference"
    "meta"
    "nativeBuildInputs"
    "moduleDir"
    "packageOverrides"
  ];

in
stdenv.mkDerivation (
  extraArgs
  // {
    inherit
      pname
      version
      src
      meta
      ;

    nativeBuildInputs = [ hooks.goAppHook ] ++ overrideNativeBuildInputs ++ nativeBuildInputs;
    buildInputs = require;

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
