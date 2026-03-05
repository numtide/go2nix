# go2nix/nix/mk-go-package-set.nix — per-package derivations from a lockfile.
#
# Reads a go2nix lockfile and produces one derivation per third-party Go
# package.  Each derivation compiles a single package via `go2nix compile-package`
# and outputs $out/<importpath>.a.
#
# Returns an attrset: { "import/path" = <derivation>; ... }
{
  goLock ? null, # path to go2nix.toml lockfile (parsed if lockfileData not given)
  lockfileData ? builtins.fromTOML (builtins.readFile goLock), # pre-parsed lockfile
  go, # Go toolchain
  go2nix, # go2nix binary (for compile-package subcommand)
  pkgs, # nixpkgs
  tags ? [], # build tags
}:
let
  helpers = import ./helpers.nix;
  inherit (helpers) parseModKey sanitizeName removePrefix escapeModPath;

  lockfile = lockfileData;
  tagFlag = if tags == [ ] then "" else builtins.concatStringsSep "," tags;

  stdlib = import ./stdlib.nix {
    inherit go;
    inherit (pkgs) runCommandCC;
  };

  importcfgFor = import ./importcfg.nix;
  compile = import ./compile.nix { go2nixBin = go2nix; inherit tagFlag; };
  fetchModule = import ./fetch-module.nix { inherit go pkgs helpers; };

  # One FOD per module.
  moduleSrcs = builtins.mapAttrs fetchModule lockfile.mod;

  # buildPackage compiles a single Go package.
  buildPackage =
    importPath: pkg:
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
      deps = map (imp: packages.${imp}) (pkg.imports or [ ]);
    in
    pkgs.runCommandCC "gopkg-${sanitizeName importPath}"
      {
        nativeBuildInputs = [
          go
        ];
      }
      ''
        export HOME=$NIX_BUILD_TOP

        # Build importcfg: stdlib + direct dependencies.
        ${importcfgFor { inherit stdlib deps; }}

        # Create output directory.
        mkdir -p "$out/$(dirname "${importPath}")"

        # Compile the package (go2nix discovers source files internally).
        ${compile.compileGoPackageInline { inherit importPath srcDir; }}
      '';

  # One derivation per package, wired by lazy attrset self-reference.
  packages = builtins.mapAttrs buildPackage lockfile.pkg;

in
packages
