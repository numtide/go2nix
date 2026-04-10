# go2nix/nix/dag/fetch-go-module.nix — fixed-output derivation to fetch a Go module.
#
# Outputs the extracted module source tree (GOMODCACHE/<escapedPath>@<escapedVersion>/).
# Cache metadata (cache/download/*.info, cache/vcs/ bare clones) is excluded so
# the NAR hash is independent of GOPROXY — `direct` adds an Origin block to
# .info and a full git clone under cache/vcs/, which would otherwise make
# lockfile hashes non-reproducible across proxies. `go2nix generate` hashes the
# same source tree (lockfilegen.hashModuleSource), and the lockfile-free path
# (plugin moduleHashes from go.sum) covers the same bytes, so both modes share
# one FOD per module@version.
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
  # Explicit GOPROXY for the FOD's `go mod download`. When null, the
  # impureEnvVars path below still lets the build environment's GOPROXY
  # bleed through (matching nixpkgs buildGoModule). Set this for private
  # modules so the proxy is part of the derivation env rather than relying
  # on the daemon's environment — under daemon nix or remote builders the
  # impure path sees no GOPROXY and falls back to proxy.golang.org,direct.
  goProxy ? null,
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

  # When goProxy is null the prefix is "", so buildPhase is byte-identical
  # and the FOD .drv path doesn't change. When set, the explicit export
  # wins over whatever impureEnvVars let through from the builder env.
  buildPhase =
    lib.optionalString (goProxy != null) ''
      export GOPROXY=${lib.escapeShellArg goProxy}
    ''
    + ''
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
    + ''
      export GOMODCACHE=$TMPDIR/modcache
      go mod download "${fetchPath}@${version}"
      cp -r "$TMPDIR/modcache/${dirSuffix}" "$out"
    '';

  # Skip other phases.
  dontInstall = true;
  dontFixup = true;
}
