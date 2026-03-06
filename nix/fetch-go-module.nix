# go2nix/nix/fetch-go-module.nix — fixed-output derivation to fetch a Go module.
#
# Downloads a module via the Go module proxy and produces the GOMODCACHE
# directory structure as output.
#
# For private modules, set netrcFile in mk-go-env.nix to provide credentials.
# Go's default GOPROXY (https://proxy.golang.org,direct) falls back to direct
# VCS access when the proxy returns 404, so netrcFile is sufficient for most
# private module setups.
{
  go,
  stdenvNoCC,
  cacert,
  helpers,
  netrcFile,
}:
let
  inherit (helpers) modKeyPath sanitizeName;
in
modKey: mod:
let
  modPath = modKeyPath modKey mod.version;
  fetchPath = mod.replaced or modPath;
in
stdenvNoCC.mkDerivation {
  name = "gomod-${sanitizeName modKey}";

  # Fixed-output derivation: content-addressed by NAR hash.
  outputHashAlgo = "sha256";
  outputHashMode = "recursive";
  outputHash = mod.hash;

  nativeBuildInputs = [
    go
    cacert
  ];

  # No source — we download in the build phase.
  dontUnpack = true;

  buildPhase = ''
    export HOME=$TMPDIR
    export GOMODCACHE=$out
    export GOSUMDB=off
    export GONOSUMCHECK='*'
    ${if netrcFile != null then ''
      cp ${netrcFile} $HOME/.netrc
      chmod 600 $HOME/.netrc
    '' else ""}
    go mod download "${fetchPath}@${mod.version}"
  '';

  # Skip other phases.
  dontInstall = true;
  dontFixup = true;
}
