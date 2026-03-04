# go2nix/nix/fetch-module.nix — fixed-output derivation to fetch a Go module.
#
# Downloads a module via the Go module proxy and produces the GOMODCACHE
# directory structure as output.
{
  go,
  pkgs,
  helpers,
}:
let
  inherit (helpers) parseModKey sanitizeName;
in
modKey: mod:
let
  parsed = parseModKey modKey;
  fetchPath = if mod ? replaced then mod.replaced else parsed.path;
in
pkgs.stdenvNoCC.mkDerivation {
  name = "gomod-${sanitizeName modKey}";

  # Fixed-output derivation: content-addressed by NAR hash.
  outputHashAlgo = "sha256";
  outputHashMode = "recursive";
  outputHash = mod.hash;

  nativeBuildInputs = [
    go
    pkgs.cacert
  ];

  # No source — we download in the build phase.
  dontUnpack = true;

  buildPhase = ''
    export HOME=$TMPDIR
    export GOMODCACHE=$out
    export GONOSUMDB='*'
    export GONOSUMCHECK='*'
    go mod download "${fetchPath}@${mod.version}"
  '';

  # Skip other phases.
  dontInstall = true;
  dontFixup = true;
}
