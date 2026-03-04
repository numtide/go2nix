# go2nix/nix/build-go-binary.nix — compile local packages and link into a binary.
#
# Uses mkDerivation with configurePhase/buildPhase/installPhase so users get
# standard hooks (preBuild, postInstall, etc.) and can pass extra nativeBuildInputs.
#
# Third-party packages come from mkGoPackageSet (cached per-package).
# Local packages are compiled from src in a single derivation.
{
  src,
  goLock ? "${src}/go2nix.toml",
  moduleDir ? ".", # relative path from src to directory containing go.mod
  go,
  go2nix,
  pkgs,
  subPackages ? [ "." ],
  pname ? "go-binary",
  version ? "0-unstable",
  tags ? [],
  ldflags ? [],
  CGO_ENABLED ? null,
  meta ? {},
  nativeBuildInputs ? [],
  ...
}@args:
let
  helpers = import ./helpers.nix;
  compile = import ./compile.nix { };

  # Parse lockfile once; share with mkGoPackageSet to avoid double fromTOML.
  lockfile = builtins.fromTOML (builtins.readFile goLock);

  # Build tag flag for go tool compile and go2nix list-files.
  tagFlag = if tags == [ ] then "" else "-tags=${builtins.concatStringsSep "," tags}";

  # Linker flags string.
  ldflagsStr = builtins.concatStringsSep " " ldflags;

  # The main module path from go.mod.
  moduleRoot = if moduleDir == "." then "${src}" else "${src}/${moduleDir}";
  goModContent = builtins.readFile "${moduleRoot}/go.mod";
  modulePath =
    let
      lines = builtins.filter (l: l != [ ] && builtins.isString l) (builtins.split "\n" goModContent);
      moduleLine = builtins.head (
        builtins.filter (l: builtins.isString l && builtins.substring 0 7 l == "module ") lines
      );
    in
    builtins.substring 7 (builtins.stringLength moduleLine - 7) moduleLine;

  # Metadata for each sub-package to build.
  subPackageMeta = map (sp: {
    subPackage = sp;
    importPath = if sp == "." then modulePath else "${modulePath}/${sp}";
    srcDir = if sp == "." then "${src}" else "${src}/${sp}";
    binName = if sp == "." then pname else builtins.baseNameOf sp;
  }) subPackages;

  # Third-party package set — pass pre-parsed lockfile to avoid re-parsing.
  packageSet = import ./mk-go-package-set.nix {
    lockfileData = lockfile;
    inherit
      go
      go2nix
      pkgs
      tags
      ;
  };

  stdlib = import ./stdlib.nix {
    inherit go;
    inherit (pkgs) runCommandCC;
  };

  # All third-party package derivations (used in importcfg for linking).
  allThirdPartyDeps = builtins.attrValues packageSet;

  # Pre-generate importcfg entries at eval time to avoid inlining shell per dep
  # (which would exceed ARG_MAX with large dependency sets).
  # Each package derivation outputs $out/<importPath>.a, so no `find` needed.
  thirdPartyImportcfg = pkgs.writeText "importcfg-third-party" (
    builtins.concatStringsSep "\n" (
      map (importPath:
        "packagefile ${importPath}=${packageSet.${importPath}}/${importPath}.a"
      ) (builtins.attrNames packageSet)
    )
  );

  # Filter out our known args so extra attrs can be passed through to mkDerivation.
  extraArgs = builtins.removeAttrs args [
    "src"
    "goLock"
    "moduleDir"
    "go"
    "go2nix"
    "pkgs"
    "subPackages"
    "pname"
    "version"
    "tags"
    "ldflags"
    "CGO_ENABLED"
    "meta"
    "nativeBuildInputs"
  ];

in
pkgs.stdenv.mkDerivation (extraArgs // {
  inherit pname version src meta;

  nativeBuildInputs = [
    go
    go2nix
    pkgs.jq
  ] ++ nativeBuildInputs;

  # --- configurePhase ---
  # Set up environment, build importcfg, define compile_go_pkg function.
  configurePhase = ''
    runHook preConfigure

    export HOME=$NIX_BUILD_TOP
    ${if CGO_ENABLED != null then "export CGO_ENABLED=${CGO_ENABLED}" else ""}

    go_os=$(go env GOOS)
    go_arch=$(go env GOARCH)

    # Define the compile_go_pkg shell function.
    ${compile.compileGoPackageFn}

    # Build importcfg with ALL packages (stdlib + third-party).
    cat "${stdlib}/importcfg" > "$NIX_BUILD_TOP/importcfg"
    cat "${thirdPartyImportcfg}" >> "$NIX_BUILD_TOP/importcfg"

    runHook postConfigure
  '';

  # --- buildPhase ---
  # Compile local library packages, then main packages + link.
  buildPhase = ''
    runHook preBuild

    localdir="$NIX_BUILD_TOP/local-pkgs"
    mkdir -p "$localdir"

    # Get all local packages in dependency order.
    localjson=$(go2nix list-local-packages ${tagFlag} "${moduleRoot}")

    # Pass 1: compile library packages (in topological order).
    while read -r pkgentry; do
      importpath=$(echo "$pkgentry" | jq -r '.import_path')
      srcdir=$(echo "$pkgentry" | jq -r '.src_dir')

      echo "Compiling local library: $importpath ($srcdir)"
      compile_go_pkg "$importpath" "$srcdir" "$localdir/$importpath.a" "$pkgentry"

      echo "packagefile $importpath=$localdir/$importpath.a" >> "$NIX_BUILD_TOP/importcfg"
    done < <(echo "$localjson" | jq -c '.[] | select(.is_command == false)')

    # Pass 2: Compile main packages and link.
    mkdir -p "$NIX_BUILD_TOP/staging/bin"

    ${builtins.concatStringsSep "\n" (
      map (meta: ''
        echo "Compiling main: ${meta.importPath} (${meta.srcDir})"
        filesjson=$(go2nix list-files ${tagFlag} "${meta.srcDir}")

        compile_go_pkg "main" "${meta.srcDir}" "$localdir/${meta.importPath}.a" "$filesjson" "main_${meta.binName}"

        linkflags=""
        if [ -f "$NIX_BUILD_TOP/.has_cgo" ]; then
          linkflags="-extld gcc -linkmode external"
        fi

        go tool link \
          -importcfg "$NIX_BUILD_TOP/importcfg" \
          ${ldflagsStr} \
          $linkflags \
          -o "$NIX_BUILD_TOP/staging/bin/${meta.binName}" \
          "$localdir/${meta.importPath}.a"
      '') subPackageMeta
    )}

    runHook postBuild
  '';

  # --- installPhase ---
  # Copy binaries from staging to $out/bin.
  installPhase = ''
    runHook preInstall

    mkdir -p "$out/bin"
    cp -r "$NIX_BUILD_TOP/staging/bin/"* "$out/bin/"

    runHook postInstall
  '';

  # Skip default unpack (we use src paths directly).
  dontUnpack = true;
  dontFixup = true;
})
