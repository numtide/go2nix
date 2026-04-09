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
# For private modules, set netrcFile in mk-go-env.nix to provide credentials.
# GOPROXY and NETRC are inherited from the build environment via impureEnvVars
# (matching nixpkgs buildGoModule for GOPROXY; cmd/go reads $NETRC directly).
{
  lib,
  go,
  stdenvNoCC,
  cacert,
  helpers,
  netrcFile,
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
  # Both path and version are case-escaped on disk (module.EscapeVersion).
  dirSuffix = "${escapedPath}@${escapeModPath version}";
in
stdenvNoCC.mkDerivation {
  name = "gomod-${sanitizeName fetchPath}-${version}";

  # Fixed-output derivation: content-addressed by NAR hash.
  outputHashAlgo = "sha256";
  outputHashMode = "recursive";
  outputHash = hash;

  # Inherit proxy configuration from the build environment. outputHash pins
  # the result regardless of which proxy serves it; this just lets the fetch
  # route through a private/caching proxy. Matches nixpkgs buildGoModule.
  impureEnvVars = lib.fetchers.proxyImpureEnvVars ++ [
    "GOPROXY"
    "NETRC"
  ];

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
      if netrcFile != null then
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
