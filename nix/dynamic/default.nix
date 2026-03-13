# go2nix/nix/build-go-application-dynamic.nix — dynamic derivation builder.
#
# Uses recursive-nix + CA derivations + dynamic derivations to eliminate [pkg]
# from the lockfile. Package graph is discovered at build time via `go list`,
# then registered as CA derivations via `nix derivation add`.
#
# The wrapper derivation is text-mode CA: its output is a .drv file.
# `builtins.outputOf` resolves it to the final binary at eval time.
#
# Usage:
#   goEnv.buildGoApplicationDynamic {
#     src = ./.;
#     goLock = ./go2nix.toml;
#     pname = "my-app";
#     version = "0.1.0";
#   }
{
  lib,
  stdenv,
  go,
  go2nix,
  nixPackage,
  coreutils,
  bash,
  cacert,
  netrcFile,
  stdlib,
}:

{
  pname,
  src,
  goLock,
  subPackages ? [ "." ],
  tags ? [ ],
  ldflags ? [ ],
  gcflags ? [ ],
  CGO_ENABLED ? null,
  nativeBuildInputs ? [ ],
  moduleDir ? ".",
  packageOverrides ? { },
  ...
}:

let
  moduleRoot = if moduleDir == "." then "${src}" else "${src}/${moduleDir}";

  # Serialize packageOverrides to JSON for the resolve command.
  # Only pass nativeBuildInputs store paths — resolve adds them to derivation inputs.
  # Auto-expand .dev outputs (like stdenv's multiple-outputs.sh hook) so users
  # can write `pkgs.pcsclite` instead of `pkgs.pcsclite.dev pkgs.pcsclite.out`.
  overridesJSON = builtins.toJSON (
    lib.mapAttrs (_path: cfg: {
      nativeBuildInputs = map toString (
        lib.concatMap (
          input:
          if builtins.isAttrs input then
            lib.unique [
              input
              (lib.getDev input)
            ]
          else
            [ input ]
        ) (cfg.nativeBuildInputs or [ ])
      );
    }) packageOverrides
  );

  wrapperDrv = stdenv.mkDerivation {
    name = "${pname}.drv";

    # Text-mode content-addressed output: the wrapper writes a .drv file to $out.
    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";

    requiredSystemFeatures = [ "recursive-nix" ];

    # Prevent self-references in text-mode output (stdenv adds -rpath with self ref).
    NIX_NO_SELF_RPATH = true;

    nativeBuildInputs = [
      go
      go2nix
      nixPackage
      coreutils
      bash
      cacert
    ]
    ++ lib.concatMap (cfg: cfg.nativeBuildInputs or [ ]) (lib.attrValues packageOverrides)
    ++ nativeBuildInputs;

    # No source to unpack — everything is passed via store paths.
    dontUnpack = true;
    dontInstall = true;
    dontFixup = true;

    buildPhase = ''
      export NIX_CONFIG="extra-experimental-features = nix-command ca-derivations dynamic-derivations"
      export HOME=$TMPDIR

      go2nix resolve \
        --src ${moduleRoot} \
        --lockfile ${goLock} \
        --system ${stdenv.hostPlatform.system} \
        --go ${go}/bin/go \
        --stdlib ${stdlib} \
        --nix ${nixPackage}/bin/nix \
        --go2nix ${go2nix}/bin/go2nix \
        --bash ${bash}/bin/bash \
        --coreutils ${coreutils}/bin/mkdir \
        --pname ${lib.escapeShellArg pname} \
        --sub-packages ${lib.escapeShellArg (lib.concatStringsSep "," subPackages)} \
        --tags ${lib.escapeShellArg (lib.concatStringsSep "," tags)} \
        --ldflags ${lib.escapeShellArg (lib.concatStringsSep " " ldflags)} \
        --overrides ${lib.escapeShellArg overridesJSON} \
        ${lib.optionalString (CGO_ENABLED != null) "--cgo-enabled ${toString CGO_ENABLED}"} \
        ${
          lib.optionalString (
            gcflags != [ ]
          ) "--gcflags ${lib.escapeShellArg (lib.concatStringsSep " " gcflags)}"
        } \
        --cacert ${cacert}/etc/ssl/certs/ca-bundle.crt \
        ${lib.optionalString (netrcFile != null) "--netrc-file ${netrcFile}"} \
        --output $out
    '';

    passthru = {
      # The final binary, resolved via dynamic derivation chain:
      # wrapper.drv → builds → .drv file → builtins.outputOf reads it → builds final binary
      target = builtins.outputOf wrapperDrv.outPath "out";

      inherit
        go
        go2nix
        goLock
        ;
    };
  };

in
wrapperDrv
