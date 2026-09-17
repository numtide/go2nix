# go2nix/nix/dag/hooks/default.nix — setup hooks for Go compilation.
#
# Three hooks:
#   setupGoEnv    — HOME and an offline Go environment; pulled in by the two below
#   goModuleHook  — compile one Go package (third-party, local or test-only).
#                   Only cgo packages go through it; pure-Go packages are
#                   compiled by rawGoCompile in ../default.nix, without stdenv
#   goAppHook     — link a binary via go2nix link-binary, and run the tests
{
  go,
  go2nix,
  makeSetupHook,
}:
let
  setupGoEnv = makeSetupHook {
    name = "go2nix-setup-go-env";
  } ./setup-go-env.sh;
in
{
  inherit setupGoEnv;

  # Hook for compiling one cgo package.
  # The variables a derivation using it must set are listed at the top of
  # compile-go-pkg.sh. Two more are optional: goSrcOverlay (a store path
  # copied over a writable copy of the source directory, from
  # packageOverrides.<path>.srcOverlay) and iface (set by Nix when the
  # derivation has an `iface` output, which then receives the .x export data
  # and the importcfg entry).
  goModuleHook = makeSetupHook {
    name = "go2nix-module-hook";
    propagatedBuildInputs = [
      go
      setupGoEnv
    ];
    substitutions = {
      go2nix = "${go2nix}/bin/go2nix";
    };
  } ./compile-go-pkg.sh;

  # Hook for building and linking Go application binaries.
  # Derivations using this hook must set:
  #   env.linkManifestJSON — link manifest content (JSON string)
  #   env.testManifestJSON — test manifest content (optional, when doCheck=true)
  goAppHook = makeSetupHook {
    name = "go2nix-app-hook";
    propagatedBuildInputs = [
      go
      setupGoEnv
    ];
    substitutions = {
      go2nix = "${go2nix}/bin/go2nix";
    };
  } ./link-go-binary.sh;
}
