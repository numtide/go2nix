# Find Go packages in nixpkgs (built with buildGoModule).
#
# Returns a list of { name, pname, version, src_url, c_libs, native_tools }
# for all packages that have goModules (i.e., built with buildGoModule).
#
# Usage:
#   nix eval --impure --json --expr 'import ./scripts/nixpkgs-go-find.nix {}'
#   nix eval --impure --json --expr 'import ./scripts/nixpkgs-go-find.nix { nixpkgsPath = /path/to/nixpkgs; }'
{
  nixpkgsPath ? <nixpkgs>,
}:
let
  pkgs = import nixpkgsPath { config.allowUnfree = true; };

  inherit (builtins)
    tryEval
    attrNames
    filter
    hasAttr
    isAttrs
    map
    concatLists
    elem
    length
    head
    ;

  # Check if a dependency list contains pkg-config.

  # Extract dependency names, excluding common Go/Nix tooling.
  depNames =
    deps:
    let
      skip = [
        "go"
        "pkg-config"
        "pkg-config-wrapper"
        "makeWrapper"
        "installShellFiles"
        "git"
        "which"
        "removeReferencesTo"
      ];
    in
    filter (n: n != "" && !(elem n skip)) (
      map (
        dep:
        let
          r = tryEval (dep.pname or "");
        in
        if r.success then r.value else ""
      ) deps
    );

  # Check a single top-level nixpkgs attribute.
  checkPkg =
    name:
    let
      rawPkg = tryEval pkgs.${name};
    in
    if !rawPkg.success then
      [ ]
    else
      let
        pkg = rawPkg.value;
      in
      if !(isAttrs pkg) then
        [ ]
      else
        let
          goModCheck = tryEval (hasAttr "goModules" pkg);
        in
        if !goModCheck.success || !goModCheck.value then
          [ ]
        else
          let
            nbiR = tryEval (pkg.nativeBuildInputs or [ ]);
            biR = tryEval (pkg.buildInputs or [ ]);
            nbi = if nbiR.success then nbiR.value else [ ];
            bi = if biR.success then biR.value else [ ];

            pnameR = tryEval (pkg.pname or name);
            versionR = tryEval (pkg.version or "unknown");
            srcUrlR = tryEval (
              pkg.src.url or (if pkg.src ? urls && length pkg.src.urls > 0 then head pkg.src.urls else "")
            );
          in
          [
            {
              inherit name;
              pname = if pnameR.success then pnameR.value else name;
              version = if versionR.success then versionR.value else "unknown";
              src_url = if srcUrlR.success then srcUrlR.value else "";
              c_libs = depNames bi;
              native_tools = depNames nbi;
            }
          ];

in
concatLists (map checkPkg (attrNames pkgs))
