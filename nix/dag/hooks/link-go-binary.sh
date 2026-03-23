# shellcheck shell=bash
# Atomic hook: link Go binaries via go2nix link-binary.
#
# Expected environment variables (set via derivation `env`):
#   linkManifestJSON  — link manifest content (JSON string)
#   testManifestJSON  — test manifest content (JSON string, only when doCheck=true)

# Variables set by Nix stdenv / derivation env, not by this script.
# shellcheck disable=SC2154
linkGoBinaryConfigurePhase() {
  runHook preConfigure
  # No-op: link-binary handles lockfile validation and modinfo computation.
  runHook postConfigure
}

linkGoBinaryBuildPhase() {
  runHook preBuild

  # Write link manifest JSON to a file for go2nix to read.
  echo "$linkManifestJSON" > "$NIX_BUILD_TOP/link-manifest.json"

  @go2nix@ link-binary \
    --manifest "$NIX_BUILD_TOP/link-manifest.json" \
    --output "$NIX_BUILD_TOP/staging"

  runHook postBuild
}

linkGoBinaryInstallPhase() {
  runHook preInstall

  mkdir -p "$out/bin"
  cp "$NIX_BUILD_TOP/staging/bin/"* "$out/bin/"

  runHook postInstall
}

linkGoBinaryCheckPhase() {
  runHook preCheck

  # Write test manifest JSON to a file for go2nix to read.
  echo "$testManifestJSON" > "$NIX_BUILD_TOP/test-manifest.json"

  @go2nix@ test-packages \
    --manifest "$NIX_BUILD_TOP/test-manifest.json"

  runHook postCheck
}

# Consumed by Nix stdenv, not by this script.
# shellcheck disable=SC2034
configurePhase=linkGoBinaryConfigurePhase
# shellcheck disable=SC2034
buildPhase=linkGoBinaryBuildPhase
# shellcheck disable=SC2034
installPhase=linkGoBinaryInstallPhase
# shellcheck disable=SC2034
checkPhase=linkGoBinaryCheckPhase
# shellcheck disable=SC2034
dontUnpack=1
