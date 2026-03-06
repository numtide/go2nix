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
  @go2nix@ check-lockfile --lockfile "$goLockfile" "$goModuleRoot"

  # Extract module path from go.mod at build time (avoids Nix eval-time parsing).
  goModulePath=$(awk '/^module /{print $2; exit}' "$goModuleRoot/go.mod")
  export goModulePath

  # Build importcfg: stdlib + all third-party deps.
  cat "@stdlib@/importcfg" >"$NIX_BUILD_TOP/importcfg"
  for dep in $buildInputs; do
    if [ -f "$dep/importcfg" ]; then
      cat "$dep/importcfg" >>"$NIX_BUILD_TOP/importcfg"
    fi
  done

  runHook postConfigure
}

linkGoBinaryBuildPhase() {
  runHook preBuild

  local localdir="$NIX_BUILD_TOP/local-pkgs"
  mkdir -p "$localdir"

  # Build gcflags argument array (empty if unset, avoids quoting issues).
  local -a gcflagArgs=()
  if [ -n "${goGcflags:-}" ]; then
    gcflagArgs=(--gcflags "$goGcflags")
  fi

  # Pass 1: compile library packages in parallel (DAG-aware).
  @go2nix@ compile-module \
    --importcfg "$NIX_BUILD_TOP/importcfg" \
    --outdir "$localdir" \
    --trimpath "$NIX_BUILD_TOP" \
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
      --importcfg "$NIX_BUILD_TOP/importcfg" \
      --import-path "main" \
      --src-dir "$srcdir" \
      --output "$localdir/$importpath.a" \
      --trimpath "$NIX_BUILD_TOP" \
      @tagArg@ \
      "${gcflagArgs[@]}"

    local linkflags=""
    if [ -f "$NIX_BUILD_TOP/.has_cgo" ]; then
      linkflags="-extld $CC -linkmode external"
    fi

    # Word splitting is intentional: goLdflags and linkflags contain
    # multiple space-separated flags.
    # shellcheck disable=SC2086
    @go@ tool link \
      -buildid=redacted \
      -importcfg "$NIX_BUILD_TOP/importcfg" \
      ${goLdflags:-} \
      $linkflags \
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
