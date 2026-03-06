# go2nix/nix2/fetch-module.nix — fixed-output derivation to fetch a Go module.
#
# Downloads a module via the Go module proxy and produces the GOMODCACHE
# directory structure as output.
{
  go,
  stdenvNoCC,
  cacert,
  helpers,
}:
let
  inherit (helpers) modKeyPath sanitizeName;
in
modKey: mod:
let
  modPath = modKeyPath modKey mod.version;
  fetchPath = if mod ? replaced then mod.replaced else modPath;
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
    go mod download "${fetchPath}@${mod.version}"
  '';

  # Skip other phases.
  dontInstall = true;
  dontFixup = true;
}
