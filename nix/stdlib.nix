# go2nix/nix/stdlib.nix — compile the Go standard library.
#
# Produces:
#   $out/<pkg>.a     for each stdlib package
#   $out/importcfg   mapping import paths to .a file paths
#
# A single derivation per (go version, goEnv) pair.
# Reference: TVL depot nix/buildGo/default.nix buildStdlib.
{
  go,
  runCommandCC,
  lib,
  # Env vars forwarded to `go install std`. This is the only cmd/go
  # invocation in go2nix — per-package compile and link use go tool
  # directly, bypassing cmd/go's env-driven source selection (GOFIPS140
  # snapshot replacement, GOEXPERIMENT overlays, etc.). Settings that
  # change which stdlib sources get compiled belong here.
  goEnv ? { },
}:
let
  # Hash goEnv into the name so distinct env configurations get distinct
  # cache entries. Values are stringified first so { X = 4; } and
  # { X = "4"; } — identical under the toString export below — share a key.
  envSuffix = lib.optionalString (goEnv != { }) (
    "-"
    + builtins.substring 0 8 (
      builtins.hashString "sha256" (builtins.toJSON (lib.mapAttrs (_: toString) goEnv))
    )
  );
  envExports = lib.concatStringsSep "\n" (
    lib.mapAttrsToList (k: v: "export ${k}=${lib.escapeShellArg (toString v)}") goEnv
  );
in
runCommandCC "go-stdlib-${go.version}${envSuffix}" { nativeBuildInputs = [ go ]; } ''
  ${envExports}
  HOME="$NIX_BUILD_TOP/home"
  mkdir -p "$HOME"

  # Copy Go source tree so we can write to it. lib/ carries toolchain
  # data cmd/go reads under certain env vars (e.g. lib/fips140/*.zip
  # for GOFIPS140 snapshot selection).
  goroot="$(go env GOROOT)"
  cp -R "$goroot/src" "$goroot/pkg" "$goroot/lib" .
  chmod -R +w .

  # Compile all stdlib packages.
  GODEBUG=installgoroot=all GOROOT="$NIX_BUILD_TOP" go install -v --trimpath std 2>&1

  # Collect .a files into $out and generate importcfg.
  mkdir -p "$out"
  cp -r pkg/*_*/* "$out"

  find "$out" -name '*.a' | sort | while read -r archive; do
    rel="''${archive#"$out/"}"
    pkg="''${rel%.a}"
    echo "packagefile $pkg=$archive"
  done > "$out/importcfg"
''
