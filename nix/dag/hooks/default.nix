# go2nix/nix/dag/hooks/default.nix — setup hooks for Go compilation.
#
# Two composite hooks:
#   goModuleHook  — compile a third-party Go package
#   goAppHook     — link a binary via go2nix link-binary
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

  # Hook for compiling third-party Go packages.
  # Derivations using this hook must set:
  #   env.goPackagePath        — import path
  #   env.goPackageSrcDir      — source directory
  #   env.compileManifestJSON  — compile manifest content (JSON string)
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
