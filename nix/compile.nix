# go2nix/nix/compile.nix — shared Go package compilation logic.
#
# Delegates to `go2nix compile-package` which handles cgo/assembly/pure-Go
# compilation internally, eliminating jq and shell control flow.
#
# go2nixBin: the go2nix derivation (must have bin/go2nix with compile-package subcommand)
{ go2nixBin, tagFlag ? "", gcflags ? [] }:
let
  tagArg = if tagFlag == "" then "" else "--tags ${tagFlag}";
  gcflagsArg = if gcflags == [] then "" else "--gcflags ${builtins.concatStringsSep " " gcflags}";
in
{
  # Shell function definition for buildGoBinary.
  # Called as: compile_go_pkg <import-path> <src-dir> <output> [unused] [unused]
  # The -p flag defaults to import-path; go2nix handles uid/cgo internally.
  compileGoPackageFn = ''
    compile_go_pkg() {
      ${go2nixBin}/bin/go2nix compile-package \
        --importcfg "$NIX_BUILD_TOP/importcfg" \
        --import-path "$1" \
        --src-dir "$2" \
        --output "$3" \
        --trimpath "$NIX_BUILD_TOP" \
        ${tagArg} \
        ${gcflagsArg}
    }
  '';

  # Nix function returning an inline shell snippet for mkGoPackageSet.
  compileGoPackageInline =
    {
      importPath,
      srcDir,
    }:
    ''
      ${go2nixBin}/bin/go2nix compile-package \
        --importcfg "$NIX_BUILD_TOP/importcfg" \
        --import-path "${importPath}" \
        --src-dir "${srcDir}" \
        --output "$out/${importPath}.a" \
        --trimpath "$NIX_BUILD_TOP" \
        ${tagArg} \
        ${gcflagsArg}
    '';
}
