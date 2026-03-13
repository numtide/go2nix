# go2nix/nix/process-lockfile.nix — pure-Nix lockfile processing.
#
# Parses a go2nix v2 lockfile and returns { modules, packages } with
# pre-computed fields (dirSuffix, subdir, drvName) so the consumer
# just iterates to create derivations.
let
  helpers = import ../helpers.nix;
  inherit (helpers) sanitizeName removePrefix escapeModPath;
in
goLock:
let
  lockfile = builtins.fromTOML (builtins.readFile goLock);
  replaces = lockfile.replace or { };
  splitModKey =
    key:
    let
      m = builtins.match "(.*)@(.*)" key;
    in
    {
      path = builtins.elemAt m 0;
      version = builtins.elemAt m 1;
    };
in
{
  modules = builtins.mapAttrs (
    modKey: hash:
    let
      mk = splitModKey modKey;
      fetchPath = replaces.${modKey} or mk.path;
    in
    {
      inherit hash fetchPath;
      inherit (mk) path version;
      dirSuffix = "${escapeModPath fetchPath}@${mk.version}";
    }
  ) lockfile.mod;

  packages = builtins.listToAttrs (
    builtins.concatLists (
      builtins.attrValues (
        builtins.mapAttrs (
          modKey: pkgMap:
          let
            mk = splitModKey modKey;
          in
          builtins.attrValues (
            builtins.mapAttrs (
              importPath: imports:
              let
                subdir = if importPath == mk.path then "" else removePrefix "${mk.path}/" importPath;
              in
              {
                name = importPath;
                value = {
                  inherit modKey subdir imports;
                  drvName = "gopkg-${sanitizeName importPath}";
                };
              }
            ) pkgMap
          )
        ) lockfile.pkg
      )
    )
  );
}
