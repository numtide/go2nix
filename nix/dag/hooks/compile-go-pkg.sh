# shellcheck shell=bash
# Atomic hook: compile a single Go package via go2nix compile-package.
#
# Used for cgo packages that need stdenv (cc-wrapper). Non-cgo packages
# use a raw derivation instead.
#
# Expected environment variables (set via derivation `env`):
#   goPackagePath        — import path of the package to compile
#   goPackageSrcDir      — absolute path to the source directory
#   compileManifestJSON  — compile manifest content (JSON string)

# Variables set by Nix stdenv / derivation env, not by this script.
# shellcheck disable=SC2154
compileGoPkgBuildPhase() {
  runHook preBuild

  # Write manifest JSON to a file for go2nix to read.
  echo "$compileManifestJSON" >"$NIX_BUILD_TOP/compile-manifest.json"

  mkdir -p "$out/$(dirname "$goPackagePath")"

  @go2nix@ compile-package \
    --manifest "$NIX_BUILD_TOP/compile-manifest.json" \
    --import-path "$goPackagePath" \
    --src-dir "$goPackageSrcDir" \
    --output "$out/$goPackagePath.a" \
    --importcfg-output "$out/importcfg" \
    --trim-path "$NIX_BUILD_TOP"

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
