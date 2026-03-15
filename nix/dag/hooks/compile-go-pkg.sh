# shellcheck shell=bash
# Atomic hook: compile a single Go package via go2nix compile-package.
#
# Expected environment variables (set via derivation `env`):
#   goPackagePath   — import path of the package to compile
#   goPackageSrcDir — absolute path to the source directory
#
# Dependencies are discovered from $buildInputs at build time.

# Variables set by Nix stdenv / derivation env, not by this script.
# shellcheck disable=SC2154
compileGoPkgBuildPhase() {
  runHook preBuild

  # Build importcfg: stdlib + dependency .a files.
  cat "@stdlib@/importcfg" >"$NIX_BUILD_TOP/importcfg"
  for dep in $buildInputs; do
    if [ -f "$dep/importcfg" ]; then
      cat "$dep/importcfg" >>"$NIX_BUILD_TOP/importcfg"
    fi
  done

  # Compile the package.
  mkdir -p "$out/$(dirname "$goPackagePath")"

  # When building PIE, pass -shared to generate position-independent code,
  # matching cmd/go's default behavior. buildMode is computed at Nix eval time
  # from stdenv.hostPlatform.go.GOOS (see hooks/default.nix).
  local -a gcflagArgs=()
  if [ "@buildMode@" = "pie" ]; then
    gcflagArgs=(--gc-flags "-shared")
  fi

  @go2nix@ compile-package \
    --import-cfg "$NIX_BUILD_TOP/importcfg" \
    --import-path "$goPackagePath" \
    --src-dir "$goPackageSrcDir" \
    --output "$out/$goPackagePath.a" \
    --trim-path "$NIX_BUILD_TOP" \
    @tagArg@ \
    "${gcflagArgs[@]}"

  # Write importcfg entry for consumers of this package.
  echo "packagefile $goPackagePath=$out/$goPackagePath.a" >"$out/importcfg"

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
