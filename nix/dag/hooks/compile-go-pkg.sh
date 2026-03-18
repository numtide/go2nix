# shellcheck shell=bash
# Atomic hook: compile a single Go package via go2nix compile-package.
#
# Expected environment variables (set via derivation `env`):
#   goPackagePath   — import path of the package to compile
#   goPackageSrcDir — absolute path to the source directory
#   goGcflags       — extra compiler flags (optional)
#
# Dependencies are discovered from $buildInputs at build time.

# Variables set by Nix stdenv / derivation env, not by this script.
# shellcheck disable=SC2154
compileGoPkgBuildPhase() {
  runHook preBuild

  # Build importcfg: stdlib + dependency .a files.
  cat "@stdlib@/importcfg" >"$NIX_BUILD_TOP/importcfg"
  for dep in ${buildInputs[@]}; do
    if [ -f "$dep/importcfg" ]; then
      cat "$dep/importcfg" >>"$NIX_BUILD_TOP/importcfg"
    fi
  done

  # Compile the package.
  mkdir -p "$out/$(dirname "$goPackagePath")"

  # Build gcflags: PIE requires -shared, then append user gcflags if present.
  # buildMode is computed at Nix eval time from stdenv.hostPlatform.go.GOOS
  # (see hooks/default.nix), matching Go's internal/platform.DefaultPIE.
  local gcflags_val="${goGcflags:-}"
  # shellcheck disable=SC2050  # @buildMode@ is substituted by makeSetupHook
  if [ "@buildMode@" = "pie" ]; then
    gcflags_val="-shared${gcflags_val:+ $gcflags_val}"
  fi
  local -a gcflagArgs=()
  if [ -n "$gcflags_val" ]; then
    gcflagArgs=(--gc-flags "$gcflags_val")
  fi

  local -a pgoArgs=()
  if [ -n "${goPgoProfile:-}" ]; then
    pgoArgs=(--pgo-profile "$goPgoProfile")
  fi

  @go2nix@ compile-package \
    --import-cfg "$NIX_BUILD_TOP/importcfg" \
    --import-path "$goPackagePath" \
    --src-dir "$goPackageSrcDir" \
    --output "$out/$goPackagePath.a" \
    --trim-path "$NIX_BUILD_TOP" \
    @tagArg@ \
    "${gcflagArgs[@]}" \
    "${pgoArgs[@]}"

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
