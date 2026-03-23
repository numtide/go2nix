# go2nix/nix/dag/hooks/default.nix — setup hooks for Go compilation.
#
# Two composite hooks:
#   goModuleHook  — compile a third-party Go package
#   goAppHook     — compile local packages and link a binary
{
  go,
  go2nix,
  stdlib,
  stdenv,
  makeSetupHook,
  tagFlag,
}:
let
  tagArg = if tagFlag == "" then "" else "--tags ${tagFlag}";
  goos = stdenv.hostPlatform.go.GOOS;
  # Match Go's internal/platform.DefaultPIE: PIE for darwin, windows, android, ios.
  buildMode =
    if
      builtins.elem goos [
        "darwin"
        "windows"
        "android"
        "ios"
      ]
    then
      "pie"
    else
      "exe";

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
  #   env.goModuleRoot  — path containing go.mod
  #   env.goSubPackages — space-separated sub-packages (default: ".")
  #   env.goLockfile    — path to go2nix.toml lockfile
  #   env.goLdflags     — linker flags (optional)
  #   env.goGcflags     — compiler flags (optional)
  #   env.goPname       — binary name for "." package (optional)
  #   buildInputs       — all third-party package derivations
  #
  # Note: goModulePath is extracted from go.mod at build time (see link-go-binary.sh).
  goAppHook = makeSetupHook {
    name = "go2nix-app-hook";
    propagatedBuildInputs = [
      go
      setupGoEnv
    ];
    substitutions = {
      go = "${go}/bin/go";
      go2nix = "${go2nix}/bin/go2nix";
      stdlib = "${stdlib}";
      inherit tagArg buildMode;
    };
  } ./link-go-binary.sh;
}
