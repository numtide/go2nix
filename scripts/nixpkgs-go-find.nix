# Find Go packages in nixpkgs that use pkg-config (cgo external lib support).
#
# Returns a list of { name, pname, version, src_url, c_libs, native_tools }
# for packages built with buildGoModule that have pkg-config in their
# nativeBuildInputs or buildInputs.
#
# Usage:
#   nix eval --impure --json --expr 'import ./scripts/find-go-pkgconfig.nix {}'
#   nix eval --impure --json --expr 'import ./scripts/find-go-pkgconfig.nix { nixpkgsPath = /path/to/nixpkgs; }'
{ nixpkgsPath ? <nixpkgs> }:
let
  pkgs = import nixpkgsPath { config.allowUnfree = true; };

  inherit (builtins)
    tryEval attrNames filter hasAttr isAttrs
    map concatLists any elem length head;

  # Check if a dependency list contains pkg-config.
  hasPkgConfig = deps:
    any (dep:
      let r = tryEval (dep.pname or "");
      in r.success && (r.value == "pkg-config" || r.value == "pkg-config-wrapper")
    ) deps;

  # Extract dependency names, excluding common Go/Nix tooling.
  depNames = deps:
    let
      skip = [
        "go" "pkg-config" "pkg-config-wrapper" "makeWrapper"
        "installShellFiles" "git" "which" "removeReferencesTo"
      ];
    in
    filter (n: n != "" && !(elem n skip)) (
      map (dep:
        let r = tryEval (dep.pname or "");
        in if r.success then r.value else ""
      ) deps
    );

  # Check a single top-level nixpkgs attribute.
  checkPkg = name:
    let
      rawPkg = tryEval pkgs.${name};
    in
    if !rawPkg.success then []
    else let pkg = rawPkg.value; in
    if !(isAttrs pkg) then []
    else let goModCheck = tryEval (hasAttr "goModules" pkg); in
    if !goModCheck.success || !goModCheck.value then []
    else let
      nbiR = tryEval (pkg.nativeBuildInputs or []);
      biR = tryEval (pkg.buildInputs or []);
      nbi = if nbiR.success then nbiR.value else [];
      bi = if biR.success then biR.value else [];
    in
    if !(hasPkgConfig (nbi ++ bi)) then []
    else let
      pnameR = tryEval (pkg.pname or name);
      versionR = tryEval (pkg.version or "unknown");
      srcUrlR = tryEval (
        pkg.src.url or
        (if pkg.src ? urls && length pkg.src.urls > 0 then head pkg.src.urls else "")
      );
    in [{
      inherit name;
      pname = if pnameR.success then pnameR.value else name;
      version = if versionR.success then versionR.value else "unknown";
      src_url = if srcUrlR.success then srcUrlR.value else "";
      c_libs = depNames bi;
      native_tools = depNames nbi;
    }];

in
concatLists (map checkPkg (attrNames pkgs))
