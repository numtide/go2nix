# go2nix/nix2/build-go-application.nix — convenience wrapper for building Go binaries.
#
# Usage:
#   goEnv.buildGoApplication {
#     pname = "my-app";
#     version = "0.1.0";
#     src = ./.;
#   }
#
# Equivalent to:
#   stdenv.mkDerivation {
#     nativeBuildInputs = [ goEnv.hooks.goAppHook ];
#     buildInputs = goEnv.require;
#     ...
#   }
{
  go,
  go2nix,
  lib,
  stdenv,
  hooks,
  helpers,
  parseGoMod,
  tags,
  tagFlag,
  lockfile ? null,
  require ? [],
  ...
}:

{
  src,
  pname ? "go-app",
  version ? "0-unstable",
  subPackages ? [ "." ],
  ldflags ? [],
  CGO_ENABLED ? null,
  meta ? {},
  nativeBuildInputs ? [],
  moduleDir ? ".",
  ...
}@args:

let
  inherit (builtins) attrNames filter hasAttr concatStringsSep;

  # Module root and go.mod parsing.
  moduleRoot = if moduleDir == "." then "${src}" else "${src}/${moduleDir}";
  goModContent = builtins.readFile "${moduleRoot}/go.mod";
  modulePath =
    let
      lines = filter (l: l != [] && builtins.isString l) (builtins.split "\n" goModContent);
      moduleLine = builtins.head (
        filter (l: builtins.isString l && builtins.substring 0 7 l == "module ") lines
      );
    in
    builtins.substring 7 (builtins.stringLength moduleLine - 7) moduleLine;

  # --- Eval-time mvscheck ---
  # Verify go.mod is consistent with the lockfile before building anything.
  goMod = parseGoMod goModContent;
  mvscheck =
    if lockfile == null then true
    else
    let
      effectiveVersion = path:
        let repl = goMod.replace.${path} or null;
        in
        if repl != null && repl ? version then repl.version
        else goMod.require.${path};

      localReplacePaths = attrNames (
        builtins.removeAttrs goMod.replace (
          filter (p: (goMod.replace.${p}) ? version) (attrNames goMod.replace)
        )
      );

      missing = filter (path:
        let
          isLocal = builtins.elem path localReplacePaths;
          key = "${path}@${effectiveVersion path}";
        in
        !isLocal && !(hasAttr key lockfile.mod)
      ) (attrNames goMod.require);
    in
    if missing == [] then true
    else throw ''

      go2nix lockfile is stale — go.mod requires modules not in lockfile:

        ${concatStringsSep "\n    " (map (p: "${p}@${effectiveVersion p}") missing)}

      Run: go mod tidy && go2nix generate
    '';

  # Linker flags string.
  ldflagsStr = concatStringsSep " " ldflags;

  # Filter out known args so extra attrs pass through to mkDerivation.
  extraArgs = builtins.removeAttrs args [
    "src"
    "pname"
    "version"
    "subPackages"
    "ldflags"
    "CGO_ENABLED"
    "meta"
    "nativeBuildInputs"
    "moduleDir"
  ];

in
assert mvscheck;
stdenv.mkDerivation (extraArgs // {
  inherit pname version src meta;

  nativeBuildInputs = [ hooks.goAppHook ] ++ nativeBuildInputs;
  buildInputs = require;

  env = {
    goModuleRoot = moduleRoot;
    goModulePath = modulePath;
    goSubPackages = concatStringsSep " " subPackages;
    goLdflags = ldflagsStr;
    goPname = pname;
  } // (if CGO_ENABLED != null then { inherit CGO_ENABLED; } else {});
})
