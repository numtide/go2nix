# shellcheck shell=bash
# Atomic hook: compile a single Go package via go2nix compile-package.
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

  # When the derivation has an `iface` output (interface split mode),
  # write the export-data-only archive there and the link object to
  # $out. The importcfg fragment goes to $iface so downstream compiles
  # depend only on the interface; the link importcfg in dag/default.nix
  # references $out/<path>.a directly.
  if [[ -n "${iface:-}" ]]; then
    mkdir -p "$iface/$(dirname "$goPackagePath")"
    @go2nix@ compile-package \
      --manifest "$NIX_BUILD_TOP/compile-manifest.json" \
      --import-path "$goPackagePath" \
      --src-dir "$goPackageSrcDir" \
      --output "$out/$goPackagePath.a" \
      --iface-output "$iface/$goPackagePath.x" \
      --importcfg-output "$iface/importcfg" \
      --trim-path "$NIX_BUILD_TOP"
  else
    @go2nix@ compile-package \
      --manifest "$NIX_BUILD_TOP/compile-manifest.json" \
      --import-path "$goPackagePath" \
      --src-dir "$goPackageSrcDir" \
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
