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

  # --- Lockfile processing: WASM fast path with pure-Nix fallback ---

  processLockfilePureNix = import ./process-lockfile.nix;

  processed =
    if builtins ? wasm then
      builtins.wasm {
        path = ./go2nix.wasm;
        function = "process_lockfile";
      } goLock
    else
      processLockfilePureNix goLock;

  # --- Third-party package set ---
  fetchModule = fetchers.fetchGoModule;

  moduleInfo = builtins.mapAttrs (
    modKey: mod:
    let
      src = fetchModule modKey { inherit (mod) hash fetchPath; };
    in
    mod // { dir = "${src}/${mod.dirSuffix}"; }
  ) processed.modules;

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
    in
    stdenv.mkDerivation {
      name = pkg.drvName;

      nativeBuildInputs = [ hooks.goModuleHook ] ++ extraNativeBuildInputs;
      buildInputs = deps;

      env = {
        goPackagePath = importPath;
        goPackageSrcDir = srcDir;
      }
      // extraEnv;
    }
  ) processed.packages;

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
    // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else { });
  }
)
