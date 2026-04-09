# go2nix/nix/dynamic/default.nix — experimental dynamic derivation builder.
#
# Uses recursive-nix + CA derivations + dynamic derivations to eliminate [pkg]
# from the lockfile. Package graph is discovered at build time via `go list`,
# then registered as CA derivations via `nix derivation add`.
#
# The wrapper derivation is text-mode CA: its output is a .drv file.
# `builtins.outputOf` resolves it to the final binary at eval time.
#
# Usage:
#   goEnv.buildGoApplicationExperimental {
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
  helpers,
  goEnv,
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
  pgoProfile ? null,
  nativeBuildInputs ? [ ],
  modRoot ? ".",
  packageOverrides ? { },
  ...
}:

assert lib.assertMsg (builtins ? outputOf)
  "go2nix dynamic mode requires the dynamic-derivations experimental feature (which implies ca-derivations). Enable with: extra-experimental-features = dynamic-derivations ca-derivations recursive-nix";

assert
  let
    # Extract major.minor from version strings like "2.34pre20260217_6e725093".
    major = lib.versions.major nixPackage.version;
    minor = lib.versions.minor nixPackage.version;
  in
  lib.assertMsg (lib.versionAtLeast "${major}.${minor}" "2.34") "go2nix dynamic mode requires Nix >= 2.34 (v4 derivation JSON format), got ${nixPackage.version}";

let
  normalizedSubPackages = helpers.normalizeSubPackages subPackages;

  # Validate packageOverrides: experimental mode only supports nativeBuildInputs.
  # Derivations are synthesized at build time by `go2nix resolve`, so env and
  # other attrs cannot be forwarded. Fail early instead of silently dropping.
  validatedOverrides = lib.mapAttrs (
    path: cfg:
    let
      knownAttrs = [ "nativeBuildInputs" ];
      unknownAttrs = builtins.attrNames (builtins.removeAttrs cfg knownAttrs);
    in
    assert
      unknownAttrs == [ ]
      || builtins.throw "packageOverrides.${path}: unknown attributes ${builtins.toJSON unknownAttrs}. Experimental mode only supports: nativeBuildInputs";
    cfg
  ) packageOverrides;

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
    }) validatedOverrides
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

    # goEnv exports reach `go2nix resolve` (which runs go list), but not the
    # synthesized per-package derivations — same limitation as packageOverrides.env
    # above. stdlib is already goEnv-aware via the scope.
    buildPhase = ''
      ${lib.concatStringsSep "\n" (
        lib.mapAttrsToList (k: v: "export ${k}=${lib.escapeShellArg (toString v)}") goEnv
      )}
      export NIX_CONFIG="extra-experimental-features = nix-command ca-derivations dynamic-derivations"
      export HOME=$TMPDIR
      # recursive-nix exports NIX_REMOTE=unix:///build/.../socket; default for set -u.
      : "''${NIX_REMOTE:=}"

      go2nix resolve \
        --src ${src} \
        --mod-root ${lib.escapeShellArg modRoot} \
        --lockfile ${goLock} \
        --system ${stdenv.buildPlatform.system} \
        --go ${go}/bin/go \
        --stdlib ${stdlib} \
        --nix ${nixPackage}/bin/nix \
        --go2nix ${go2nix}/bin/go2nix \
        --bash ${bash}/bin/bash \
        --coreutils ${coreutils}/bin/mkdir \
        --pname ${lib.escapeShellArg pname} \
        --sub-packages ${lib.escapeShellArg (lib.concatStringsSep "," normalizedSubPackages)} \
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
        ${lib.optionalString (pgoProfile != null) "--pgo-profile ${pgoProfile}"} \
        --daemon-socket "''${NIX_REMOTE#unix://}" \
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
