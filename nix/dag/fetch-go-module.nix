# go2nix/nix/dag/fetch-go-module.nix — fixed-output derivation to fetch a Go module.
#
# Two output modes controlled by `sourceOnly`:
#
#   sourceOnly = true (lockfile-free):
#     Outputs only the extracted source tree. Cache metadata (.info, .zip,
#     .ziphash, sumdb/) is excluded so the NAR hash depends solely on module
#     content — matching the h1: hash from go.sum.
#
#   sourceOnly = false (lockfile, default):
#     Outputs the full GOMODCACHE directory (backward-compatible with existing
#     lockfile hashes).
#
# For private modules, set netrcFile or netrcContent in mk-go-env.nix.
#
# netrcFile: a Nix path or store path string. Interpolated into the build
#   script as `cp ${netrcFile}` — the path must be visible inside the FOD
#   sandbox (store paths are; raw host paths like "/root/.netrc" are not).
#
# netrcContent: the literal file contents. Passed via env var and written
#   in the build phase. Use this when reading credentials from a host path
#   at eval time: `netrcContent = builtins.readFile /path/to/.netrc`.
#   Contents become part of the .drv hash but FODs are output-addressed so
#   this doesn't affect caching.
{
  lib,
  go,
  stdenvNoCC,
  cacert,
  helpers,
  netrcFile,
  netrcContent ? null,
}:
let
  inherit (helpers) sanitizeName escapeModPath;
in
{
  hash,
  fetchPath,
  version,
  sourceOnly ? false,
}:
let
  escapedPath = escapeModPath fetchPath;
  dirSuffix = "${escapedPath}@${version}";
in
stdenvNoCC.mkDerivation (
  {
    name = "gomod-${sanitizeName fetchPath}-${version}";

    # Fixed-output derivation: content-addressed by NAR hash.
    outputHashAlgo = "sha256";
    outputHashMode = "recursive";
    outputHash = hash;

    nativeBuildInputs = [
      go
      cacert
    ];

    # No source — we download in the build phase.
    dontUnpack = true;

    buildPhase = ''
      export HOME=$TMPDIR
      export GOSUMDB=off
      export GONOSUMCHECK='*'
      ${
        if netrcContent != null then
          ''
            printf '%s' "$NETRC_CONTENT" > $HOME/.netrc
            chmod 600 $HOME/.netrc
          ''
        else if netrcFile != null then
          ''
            cp ${netrcFile} $HOME/.netrc
            chmod 600 $HOME/.netrc
          ''
        else
          ""
      }
    ''
    + (
      if sourceOnly then
        ''
          export GOMODCACHE=$TMPDIR/modcache
          go mod download "${fetchPath}@${version}"
          cp -r "$TMPDIR/modcache/${dirSuffix}" "$out"
        ''
      else
        ''
          export GOMODCACHE=$out
          go mod download "${fetchPath}@${version}"
        ''
    );

    # Skip other phases.
    dontInstall = true;
    dontFixup = true;
  }
  // lib.optionalAttrs (netrcContent != null) {
    NETRC_CONTENT = netrcContent;
  }
)
