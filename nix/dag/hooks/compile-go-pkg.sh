# shellcheck shell=bash
# Atomic hook: compile a single Go package via go2nix compile-package.
#
# Used for cgo packages that need stdenv (cc-wrapper). Non-cgo packages
# use a raw derivation instead.
#
# Expected environment variables (set via derivation `env`):
#   goPackagePath        — import path of the package to compile
#   goPackageSrcDir      — absolute path to the source directory
#   goLangVersion        — module's go directive (major.minor) for -lang; may be empty
#   goModulePath         — owning module's path (for -trimpath rewrite)
#   goModuleVersion      — owning module's version; empty for main-module packages
#   compileManifestJSON  — compile manifest content (JSON string)

# Variables set by Nix stdenv / derivation env, not by this script.
# shellcheck disable=SC2154
compileGoPkgBuildPhase() {
  runHook preBuild

  # Write manifest JSON to a file for go2nix to read.
  echo "$compileManifestJSON" >"$NIX_BUILD_TOP/compile-manifest.json"

  # packageOverrides.<pkg>.srcOverlay: build-time-generated files (typically
  # //go:embed targets). Layer the overlay onto a writable copy so
  # ResolveEmbedCfg sees the generated content.
  if [[ -n ${goSrcOverlay:-} ]]; then
    cp -rL --no-preserve=mode "$goPackageSrcDir" "$NIX_BUILD_TOP/srcdir"
    cp -rL --no-preserve=mode "$goSrcOverlay"/. "$NIX_BUILD_TOP/srcdir/"
    goPackageSrcDir="$NIX_BUILD_TOP/srcdir"
  fi

  mkdir -p "$out/$(dirname "$goPackagePath")"

  # When the derivation has an `iface` output (interface split mode),
  # write the export-data-only archive there and the link object to
  # $out. The importcfg fragment goes to $iface so downstream compiles
  # depend only on the interface.
  if [[ -n ${iface:-} ]]; then
    mkdir -p "$iface/$(dirname "$goPackagePath")"
    @go2nix@ compile-package \
      --manifest "$NIX_BUILD_TOP/compile-manifest.json" \
      --import-path "$goPackagePath" \
      --src-dir "$goPackageSrcDir" \
      --go-version "$goLangVersion" \
      --module-path "$goModulePath" \
      --module-version "$goModuleVersion" \
      --output "$out/$goPackagePath.a" \
      --iface-output "$iface/$goPackagePath.x" \
      --importcfg-output "$iface/importcfg" \
      --trim-path "$NIX_BUILD_TOP"
  else
    @go2nix@ compile-package \
      --manifest "$NIX_BUILD_TOP/compile-manifest.json" \
      --import-path "$goPackagePath" \
      --src-dir "$goPackageSrcDir" \
      --go-version "$goLangVersion" \
      --module-path "$goModulePath" \
      --module-version "$goModuleVersion" \
      --output "$out/$goPackagePath.a" \
      --importcfg-output "$out/importcfg" \
      --trim-path "$NIX_BUILD_TOP"
  fi

  runHook postBuild
}

# Consumed by Nix stdenv, not by this script.
# shellcheck disable=SC2034
buildPhase=compileGoPkgBuildPhase
# shellcheck disable=SC2034
dontUnpack=1
# shellcheck disable=SC2034
dontInstall=1
# shellcheck disable=SC2034
dontFixup=1
