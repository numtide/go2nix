# go2nix/nix2/hooks/default.nix — setup hooks for Go compilation.
#
# Three composite hooks following gobuild.nix's pattern:
#   goModuleHook  — compile a third-party Go package
#   goPackageHook — compile a local library package
#   goAppHook     — compile local packages and link a binary
{ go, go2nix, stdlib, lib, makeSetupHook, tagFlag }:
let
  tagArg = if tagFlag == "" then "" else "--tags ${tagFlag}";

  setupGoEnv = makeSetupHook {
    name = "go2nix-setup-go-env";
  } ./setup-go-env.sh;
in
{
  inherit setupGoEnv;

  # Hook for compiling third-party Go packages.
  # Derivations using this hook must set:
  #   env.goPackagePath   — import path
  #   env.goPackageSrcDir — source directory
  #   buildInputs         — dependency package derivations
  goModuleHook = makeSetupHook {
    name = "go2nix-module-hook";
    propagatedBuildInputs = [ go setupGoEnv ];
    substitutions = {
      go2nix = "${go2nix}/bin/go2nix";
      stdlib = "${stdlib}";
      inherit tagArg;
    };
  } ./compile-go-pkg.sh;

  # Hook for compiling local library packages.
  # Same interface as goModuleHook; sources come from the project.
  goPackageHook = makeSetupHook {
    name = "go2nix-package-hook";
    propagatedBuildInputs = [ go setupGoEnv ];
    substitutions = {
      go2nix = "${go2nix}/bin/go2nix";
      stdlib = "${stdlib}";
      inherit tagArg;
    };
  } ./compile-go-pkg.sh;

  # Hook for building and linking Go application binaries.
  # Derivations using this hook must set:
  #   env.goModuleRoot  — path containing go.mod
  #   env.goModulePath  — Go module path (from go.mod)
  #   env.goSubPackages — space-separated sub-packages (default: ".")
  #   env.goLdflags     — linker flags (optional)
  #   env.goPname       — binary name for "." package (optional)
  #   buildInputs       — all third-party package derivations
  goAppHook = makeSetupHook {
    name = "go2nix-app-hook";
    propagatedBuildInputs = [ go setupGoEnv ];
    substitutions = {
      go = "${go}/bin/go";
      go2nix = "${go2nix}/bin/go2nix";
      stdlib = "${stdlib}";
      inherit tagArg;
    };
  } ./link-go-binary.sh;
}
