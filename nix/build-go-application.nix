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
  helpers,
  fetchers,
  ...
}:

{
  src,
  goLock,
  pname,
  version,
  subPackages ? [ "." ],
  ldflags ? [ ],
  gcflags ? [ ],
  CGO_ENABLED ? null,
  allowGoReference ? false,
  meta ? { },
  nativeBuildInputs ? [ ],
  moduleDir ? ".",
  packageOverrides ? { },
  ...
}@args:

let
  inherit (builtins) concatStringsSep;
  inherit (helpers)
    modKeyPath
    sanitizeName
    removePrefix
    escapeModPath
    ;

  lockfile = builtins.fromTOML (builtins.readFile goLock);

  # --- Third-party package set ---
  fetchModule = fetchers.fetchGoModule;
  moduleSrcs = builtins.mapAttrs fetchModule lockfile.mod;

  # Pre-compute per-module data so packages sharing a module reuse the same thunk.
  moduleInfo = builtins.mapAttrs (
    modKey: mod:
    let
      modPath = modKeyPath modKey mod.version;
      modSrc = moduleSrcs.${modKey};
      fetchPath = mod.replaced or modPath;
    in
    {
      path = modPath;
      inherit (mod) version;
      dir = "${modSrc}/${escapeModPath fetchPath}@${mod.version}";
    }
  ) lockfile.mod;

  packages = builtins.mapAttrs (
    importPath: pkg:
    let
      modKey = pkg.module;
      minfo = moduleInfo.${modKey};

      subdir = if importPath == minfo.path then "" else removePrefix "${minfo.path}/" importPath;
      srcDir = if subdir == "" then minfo.dir else "${minfo.dir}/${subdir}";

      # Direct dependency derivations (resolved lazily via Nix's laziness).
      deps = map (imp: packages.${imp}) (pkg.imports or [ ]);

      # Per-package overrides (e.g., nativeBuildInputs for cgo libraries).
      pkgOverride = packageOverrides.${importPath} or packageOverrides.${minfo.path} or { };
      extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [ ];
      extraEnv = builtins.removeAttrs pkgOverride [ "nativeBuildInputs" ];
    in
    stdenv.mkDerivation {
      name = "gopkg-${sanitizeName importPath}";

      nativeBuildInputs = [ hooks.goModuleHook ] ++ extraNativeBuildInputs;
      buildInputs = deps;

      env = {
        goPackagePath = importPath;
        goPackageSrcDir = srcDir;
      }
      // extraEnv;
    }
  ) lockfile.pkg;

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
    "ldflags"
    "gcflags"
    "CGO_ENABLED"
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
      inherit go go2nix goLock packages;
    };

    env = {
      goModuleRoot = moduleRoot;
      goSubPackages = concatStringsSep " " subPackages;
      goLdflags = ldflagsStr;
      goGcflags = gcflagsStr;
      goLockfile = "${goLock}";
      goPname = pname;
    }
    // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { });
  }
)
