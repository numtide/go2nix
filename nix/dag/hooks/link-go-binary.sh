# shellcheck shell=bash
# Atomic hook: compile local packages and link Go binaries.
#
# Expected environment variables (set via derivation `env`):
#   goModuleRoot    — absolute path to module root (containing go.mod)
#   goSubPackages   — space-separated list of sub-packages (default: ".")
#   goLdflags       — extra linker flags (optional)
#   goGcflags       — extra compiler flags (optional)
#   goLockfile      — path to go2nix.toml lockfile
#   goPname         — binary name for "." package (optional)
#
# Third-party dependencies are discovered from $buildInputs at build time.

# Variables set by Nix stdenv / derivation env, not by this script.
# shellcheck disable=SC2154
linkGoBinaryConfigurePhase() {
  runHook preConfigure

  # Validate lockfile consistency with go.mod.
  @go2nix@ check --lockfile "$goLockfile" "$goModuleRoot"

  # Extract module path from go.mod at build time (avoids Nix eval-time parsing).
  goModulePath=$(awk '/^module /{print $2; exit}' "$goModuleRoot/go.mod")
  export goModulePath

  # Build importcfg: stdlib + all third-party deps + modinfo.
  cat "@stdlib@/importcfg" >"$NIX_BUILD_TOP/importcfg"
  for dep in $buildInputs; do
    if [ -f "$dep/importcfg" ]; then
      cat "$dep/importcfg" >>"$NIX_BUILD_TOP/importcfg"
    fi
  done
  # Embed module info so go version -m shows dependencies.
  @go2nix@ build-modinfo --lockfile "$goLockfile" --go "@go@" "$goModuleRoot" \
    >>"$NIX_BUILD_TOP/importcfg"

  runHook postConfigure
}

linkGoBinaryBuildPhase() {
  runHook preBuild

  local localdir="$NIX_BUILD_TOP/local-pkgs"
  mkdir -p "$localdir"

  # Build mode is computed at Nix eval time from stdenv.hostPlatform.go.GOOS
  # (see hooks/default.nix), matching Go's internal/platform.DefaultPIE.
  local go_buildmode="@buildMode@"

  # Build gcflags argument array (empty if unset, avoids quoting issues).
  local gcflags_val="${goGcflags:-}"
  if [ "$go_buildmode" = "pie" ]; then
    gcflags_val="-shared${gcflags_val:+ $gcflags_val}"
  fi
  local -a gcflagArgs=()
  if [ -n "$gcflags_val" ]; then
    gcflagArgs=(--gc-flags "$gcflags_val")
  fi

  # Pass 1: compile library packages in parallel (DAG-aware).
  @go2nix@ compile-packages \
    --import-cfg "$NIX_BUILD_TOP/importcfg" \
    --out-dir "$localdir" \
    --trim-path "$NIX_BUILD_TOP" \
    @tagArg@ \
    "${gcflagArgs[@]}" \
    "$goModuleRoot"

  # Pass 2: compile main packages and link.
  mkdir -p "$NIX_BUILD_TOP/staging/bin"

  for sp in ${goSubPackages:-.}; do
    local importpath srcdir binname

    if [ "$sp" = "." ]; then
      importpath="$goModulePath"
      srcdir="$goModuleRoot"
      binname="${goPname:-$(basename "$goModulePath")}"
    else
      importpath="$goModulePath/$sp"
      srcdir="$goModuleRoot/$sp"
      binname="$(basename "$sp")"
    fi

    echo "Compiling main: $importpath"
    @go2nix@ compile-package \
      --import-cfg "$NIX_BUILD_TOP/importcfg" \
      --import-path "main" \
      --src-dir "$srcdir" \
      --output "$localdir/$importpath.a" \
      --trim-path "$NIX_BUILD_TOP" \
      @tagArg@ \
      "${gcflagArgs[@]}"

    local linkflags=""
    if [ -f "$NIX_BUILD_TOP/.has_cgo" ]; then
      # Use CXX when C++ files are present, matching Go's setextld (gc.go).
      local extld="$CC"
      if [ -f "$NIX_BUILD_TOP/.has_cxx" ]; then
        extld="$CXX"
      fi
      linkflags="-extld $extld -linkmode external"
    fi

    # Propagate sanitizer flags (-race, -msan, -asan) from gcflags to the
    # linker, matching cmd/go behavior (init.go forcedLdflags).
    local sanitizer_linkflags=""
    for flag in ${goGcflags:-}; do
      case "$flag" in
        -race|-msan|-asan) sanitizer_linkflags="$sanitizer_linkflags $flag" ;;
      esac
    done

    # Word splitting is intentional: goLdflags, linkflags, and
    # sanitizer_linkflags contain multiple space-separated flags.
    # shellcheck disable=SC2086
    @go@ tool link \
      -buildid=redacted \
      -buildmode="$go_buildmode" \
      -importcfg "$NIX_BUILD_TOP/importcfg" \
      ${goLdflags:-} \
      $linkflags \
      $sanitizer_linkflags \
      -o "$NIX_BUILD_TOP/staging/bin/$binname" \
      "$localdir/$importpath.a"
  done

  runHook postBuild
}

linkGoBinaryInstallPhase() {
  runHook preInstall

  mkdir -p "$out/bin"
  cp "$NIX_BUILD_TOP/staging/bin/"* "$out/bin/"

  runHook postInstall
}

# Consumed by Nix stdenv, not by this script.
# shellcheck disable=SC2034
configurePhase=linkGoBinaryConfigurePhase
# shellcheck disable=SC2034
buildPhase=linkGoBinaryBuildPhase
# shellcheck disable=SC2034
installPhase=linkGoBinaryInstallPhase
# shellcheck disable=SC2034
dontUnpack=1
