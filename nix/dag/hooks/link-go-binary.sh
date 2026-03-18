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

  # Build importcfg: stdlib + all third-party deps.
  # NOTE: modinfo is a linker-only directive (cmd/link) and must NOT be present
  # during compilation (cmd/compile rejects unknown directives). It is appended
  # to the importcfg in the build phase, after compile-packages and before link.
  cat "@stdlib@/importcfg" >"$NIX_BUILD_TOP/importcfg"
  for dep in $buildInputs; do
    if [ -f "$dep/importcfg" ]; then
      cat "$dep/importcfg" >>"$NIX_BUILD_TOP/importcfg"
    fi
  done

  # Compute module info (modinfo) and GODEBUG defaults.
  # build-modinfo outputs:
  #   modinfo "..." — for go tool link importcfg
  #   godebug <value> — for -X=runtime.godebugDefault= linker flag
  local buildinfo_out
  buildinfo_out=$(@go2nix@ build-modinfo --lockfile "$goLockfile" --go "@go@" "$goModuleRoot")
  goModinfo=$(echo "$buildinfo_out" | grep '^modinfo ' || true)
  goGodebugDefault=$(echo "$buildinfo_out" | sed -n 's/^godebug //p')

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

  local -a pgoArgs=()
  if [ -n "${goPgoProfile:-}" ]; then
    pgoArgs=(--pgo-profile "$goPgoProfile")
  fi

  # Pass 1: compile library packages in parallel (DAG-aware).
  @go2nix@ compile-packages \
    --import-cfg "$NIX_BUILD_TOP/importcfg" \
    --out-dir "$localdir" \
    --trim-path "$NIX_BUILD_TOP" \
    @tagArg@ \
    "${gcflagArgs[@]}" \
    "${pgoArgs[@]}" \
    "$goModuleRoot"

  # Resolve the linker binary before clearing GOROOT.
  local goLinkTool
  goLinkTool="$(@go@ env GOTOOLDIR)/link"

  # Do not set GOROOT: the linker reads it from os.Getenv (buildcfg/cfg.go:23)
  # and embeds it as runtime.defaultGOROOT (cmd/link main.go:180-186).
  # We cannot use `go tool link` because the go command re-exports GOROOT
  # from its binary path (cmd/go/main.go:305-311), overriding any empty value.
  # Invoking the linker directly matches what `go build -trimpath` does
  # internally (gc.go:676-678).
  export GOROOT=""

  # Compute link-time flags (invariant across sub-packages).
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
    -race | -msan | -asan) sanitizer_linkflags="$sanitizer_linkflags $flag" ;;
    esac
  done

  # GODEBUG default from go.mod's go directive (gc.go:624-626).
  local godebug_linkflag=""
  if [ -n "${goGodebugDefault:-}" ]; then
    godebug_linkflag="-X=runtime.godebugDefault=$goGodebugDefault"
  fi

  # Build linker importcfg: compile importcfg + modinfo (linker-only directive).
  cp "$NIX_BUILD_TOP/importcfg" "$NIX_BUILD_TOP/importcfg.link"
  if [ -n "${goModinfo:-}" ]; then
    echo "$goModinfo" >>"$NIX_BUILD_TOP/importcfg.link"
  fi

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
      "${gcflagArgs[@]}" \
      "${pgoArgs[@]}"

    # Word splitting is intentional: goLdflags, linkflags,
    # sanitizer_linkflags, and godebug_linkflag contain space-separated flags.
    # shellcheck disable=SC2086
    "$goLinkTool" \
      -buildid=redacted \
      -buildmode="$go_buildmode" \
      -importcfg "$NIX_BUILD_TOP/importcfg.link" \
      ${goLdflags:-} \
      $linkflags \
      $sanitizer_linkflags \
      $godebug_linkflag \
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
