# golangci-lint check for go/go2nix
#
# Overrides the go2nix package build to run golangci-lint instead of
# compiling. This reuses the vendored dependency fetching from
# buildGoModule so golangci-lint can resolve imports.
{
  pkgs,
  go2nix,
}:
let
  inherit (pkgs) golangci-lint;
in
go2nix.overrideAttrs (old: {
  pname = "golangci-lint-go2nix";

  nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [ golangci-lint ];

  # The .golangci.yml lives at the repo root, outside the Go module src.
  # Pass it in so we can copy it into the build directory.
  golangciConfig = ../../.golangci.yml;

  buildPhase = ''
    runHook preBuild
    cp $golangciConfig .golangci.yml
    export GOLANGCI_LINT_CACHE=$TMPDIR/golangci-lint-cache
    golangci-lint run --config=.golangci.yml ./...
    runHook postBuild
  '';

  installPhase = ''
    touch $out
  '';

  # Skip all the Go module build/install/check phases.
  subPackages = [ ];
  doCheck = false;
})
