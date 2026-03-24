# golangci-lint check for packages/go2nix-testgen
#
# Uses buildGoModule to set up the Go toolchain, then runs golangci-lint
# instead of compiling. testgen has no external dependencies.
{ pkgs }:
let
  go = pkgs.go_1_25;
  buildGoModule = pkgs.buildGoModule.override { inherit go; };
  inherit (pkgs) golangci-lint;
in
(buildGoModule {
  pname = "golangci-lint-go2nix-testgen";
  version = "0-unstable";

  src = pkgs.lib.sources.cleanSource ../../packages/go2nix-testgen;

  vendorHash = null;

  meta.description = "golangci-lint check for go2nix-testgen";
}).overrideAttrs
  (old: {
    nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [ golangci-lint ];

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

    subPackages = [ ];
    doCheck = false;
  })
