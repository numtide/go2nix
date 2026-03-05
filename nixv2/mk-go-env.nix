# go2nix/nix2/mk-go-env.nix — entry point: creates a Go package scope from a lockfile.
#
# Returns a scope with:
#   goEnv.buildGoApplication { ... }   — convenience wrapper (99% of use cases)
#   goEnv.go / go2nix / stdlib         — toolchain
#   goEnv.hooks.goModuleHook           — for third-party modules
#   goEnv.hooks.goPackageHook          — for local library packages
#   goEnv.hooks.goAppHook              — for application binaries
#   goEnv.fetchers.fetchGoModule       — FOD for downloading modules
#   goEnv.require                      — all third-party package derivations
#   goEnv."github.com/foo/bar"         — individual package derivations
{
  goLock,                          # path to go2nix.toml
  go,                              # Go toolchain
  go2nix,                          # go2nix binary
  callPackage,                     # nixpkgs callPackage
  lib,                             # nixpkgs lib
  overridePackage ? drv: drv,      # optional per-package override
  tags ? [],                       # build tags
  packageOverrides ? {},           # per-package overrides keyed by import path
}:
let
  helpers = import ./helpers.nix;
  inherit (helpers) parseModKey sanitizeName removePrefix escapeModPath;

  lockfile = builtins.fromTOML (builtins.readFile goLock);

  baseScope = callPackage ./scope.nix { inherit go go2nix tags; };

  overlay = final: prev:
    let
      fetchModule = final.fetchers.fetchGoModule;

      # One FOD per module.
      moduleSrcs = builtins.mapAttrs fetchModule lockfile.mod;

      # One derivation per package.
      packages = builtins.mapAttrs (importPath: pkg:
        let
          modKey = pkg.module;
          mod = lockfile.mod.${modKey};
          modSrc = moduleSrcs.${modKey};
          parsed = parseModKey modKey;

          # The actual path in GOMODCACHE where source files live.
          fetchPath = if mod ? replaced then mod.replaced else parsed.path;
          modDir = "${modSrc}/${escapeModPath fetchPath}@${parsed.version}";

          # Subdirectory within the module for this specific package.
          subdir = if importPath == parsed.path then "" else removePrefix "${parsed.path}/" importPath;
          srcDir = if subdir == "" then modDir else "${modDir}/${subdir}";

          # Direct dependency derivations (resolved lazily via Nix's laziness).
          deps = map (imp: final.${imp}) (pkg.imports or []);

          # Per-package overrides (e.g., nativeBuildInputs for cgo libraries).
          pkgOverride = packageOverrides.${importPath} or {};
          extraNativeBuildInputs = pkgOverride.nativeBuildInputs or [];
          extraEnv = builtins.removeAttrs pkgOverride [ "nativeBuildInputs" ];
        in
        overridePackage (final.callPackage ({ stdenv, hooks }:
          stdenv.mkDerivation {
            name = "gopkg-${sanitizeName importPath}";

            nativeBuildInputs = [ hooks.goModuleHook ] ++ extraNativeBuildInputs;
            buildInputs = deps;

            env = {
              goPackagePath = importPath;
              goPackageSrcDir = srcDir;
            } // extraEnv;
          }
        ) {})
      ) lockfile.pkg;
    in
    {
      inherit lockfile;
      require = builtins.attrValues packages;
    } // packages;
in
baseScope.overrideScope overlay
