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
  go2nix, # go2nix binary (for list-files, list-local-packages, compile-package)
  pkgs,
  subPackages ? [ "." ],
  pname ? "go-binary",
  version ? "0-unstable",
  tags ? [],
  ldflags ? [],
  CGO_ENABLED ? null,
  packageOverrides ? {},
  meta ? {},
  nativeBuildInputs ? [],
  ...
}@args:
let
  inherit (builtins) attrNames filter hasAttr concatStringsSep;
  helpers = import ./helpers.nix;
  parseGoMod = import ./go-mod-parser.nix;

  # Parse lockfile once; share with mkGoPackageSet to avoid double fromTOML.
  lockfile = builtins.fromTOML (builtins.readFile goLock);

  # Build tag flag for go2nix subcommands.
  tagFlag = if tags == [ ] then "" else builtins.concatStringsSep "," tags;
  tagShellArg = if tagFlag == "" then "" else "-tags ${tagFlag}";

  compile = import ./compile.nix { go2nixBin = go2nix; inherit tagFlag; };

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

  # --- Eval-time mvscheck ---
  # Verify go.mod is consistent with the lockfile before building anything.
  # For each non-local-replaced module in go.mod's require block, check that
  # module@version exists in the lockfile. Catches stale lockfiles and untidy
  # go.mod at eval time with a clear error message.
  goMod = parseGoMod goModContent;
  mvscheck =
    let
      # For a remotely replaced module, the effective version is the replace's
      # version. Local replaces (path only, no version) are skipped.
      effectiveVersion = path:
        let repl = goMod.replace.${path} or null;
        in
        if repl != null && repl ? version then repl.version
        else goMod.require.${path};

      localReplacePaths = attrNames (
        builtins.removeAttrs goMod.replace (
          filter (p: (goMod.replace.${p}) ? version) (attrNames goMod.replace)
        )
      );

      missing = filter (path:
        let
          isLocal = builtins.elem path localReplacePaths;
          key = "${path}@${effectiveVersion path}";
        in
        !isLocal && !(hasAttr key lockfile.mod)
      ) (attrNames goMod.require);
    in
    if missing == [ ] then true
    else throw ''

      go2nix lockfile is stale — go.mod requires modules not in lockfile:

        ${concatStringsSep "\n    " (map (p: "${p}@${effectiveVersion p}") missing)}

      Run: go mod tidy && go2nix generate
    '';

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
      packageOverrides
      ;
  };

  stdlib = import ./stdlib.nix {
    inherit go;
    inherit (pkgs) runCommandCC;
  };

  # All third-party package derivations (used in importcfg for linking).
  allThirdPartyDeps = builtins.attrValues packageSet;

  # Collect nativeBuildInputs from all packageOverrides so C libraries are
  # available at link time (the final binary needs to link against them).
  overrideNativeBuildInputs = builtins.concatLists (
    map (attrs: attrs.nativeBuildInputs or [])
    (builtins.attrValues packageOverrides)
  );

  # Pre-generate importcfg entries at eval time to avoid inlining shell per dep
  # (which would exceed ARG_MAX with large dependency sets).
  # Each package derivation outputs $out/<importPath>.a, so no `find` needed.
  thirdPartyImportcfg = pkgs.writeText "importcfg-third-party" (
    builtins.concatStringsSep "\n" (
      map (importPath:
        "packagefile ${importPath}=${packageSet.${importPath}}/${importPath}.a"
      ) (builtins.attrNames packageSet)
    ) + "\n"
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
    "packageOverrides"
    "meta"
    "nativeBuildInputs"
  ];

in
assert mvscheck;
pkgs.stdenv.mkDerivation (extraArgs // {
  inherit pname version src meta;

  nativeBuildInputs = [
    go
  ] ++ overrideNativeBuildInputs ++ nativeBuildInputs;

  # --- configurePhase ---
  # Set up environment, build importcfg, define compile_go_pkg function.
  configurePhase = ''
    runHook preConfigure

    export HOME=$NIX_BUILD_TOP
    ${if CGO_ENABLED != null then "export CGO_ENABLED=${CGO_ENABLED}" else ""}

    # Define the compile_go_pkg shell function (delegates to go2nix compile-package).
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
    localjson=$(${go2nix}/bin/go2nix list-local-packages ${tagShellArg} "${moduleRoot}")

    # Pass 1: compile library packages (in topological order).
    while read -r pkgentry; do
      importpath=$(echo "$pkgentry" | ${pkgs.jq}/bin/jq -r '.import_path')
      srcdir=$(echo "$pkgentry" | ${pkgs.jq}/bin/jq -r '.src_dir')

      echo "Compiling local library: $importpath ($srcdir)"
      compile_go_pkg "$importpath" "$srcdir" "$localdir/$importpath.a" "" ""

      echo "packagefile $importpath=$localdir/$importpath.a" >> "$NIX_BUILD_TOP/importcfg"
    done < <(echo "$localjson" | ${pkgs.jq}/bin/jq -c '.[] | select(.is_command == false)')

    # Pass 2: Compile main packages and link.
    mkdir -p "$NIX_BUILD_TOP/staging/bin"

    ${builtins.concatStringsSep "\n" (
      map (meta: ''
        echo "Compiling main: ${meta.importPath} (${meta.srcDir})"

        compile_go_pkg "main" "${meta.srcDir}" "$localdir/${meta.importPath}.a" "" "main_${meta.binName}"

        linkflags=""
        if [ -f "$NIX_BUILD_TOP/.has_cgo" ]; then
          linkflags="-extld $CC -linkmode external"
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
