# go2nix/nix/stdlib.nix — compile the Go standard library.
#
# Produces:
#   $out/<pkg>.a     for each stdlib package
#   $out/importcfg   mapping import paths to .a file paths
#
# This is a single derivation shared by all builds using the same Go version
# and build flags (race, etc.).
# Reference: TVL depot nix/buildGo/default.nix buildStdlib.
{
  go,
  runCommandCC,
  goInstallFlags ? [],
}:
let
  flagsStr = builtins.concatStringsSep " " goInstallFlags;
  # Include flags in derivation name for cache separation.
  suffix = if goInstallFlags == [] then "" else "-${builtins.hashString "md5" flagsStr}";
in
runCommandCC "go-stdlib-${go.version}${suffix}" { nativeBuildInputs = [ go ]; } ''
  HOME=$NIX_BUILD_TOP/home
  mkdir -p $HOME

  # Copy Go source tree so we can write to it.
  goroot="$(go env GOROOT)"
  cp -R "$goroot/src" "$goroot/pkg" .
  chmod -R +w .

  # Compile all stdlib packages.
  GODEBUG=installgoroot=all GOROOT=$NIX_BUILD_TOP go install -v --trimpath ${flagsStr} std 2>&1

  # Collect .a files into $out and generate importcfg.
  mkdir -p $out
  cp -r pkg/*_*/* $out

  find $out -name '*.a' | sort | while read -r archive; do
    rel="''${archive#"$out/"}"
    pkg="''${rel%.a}"
    echo "packagefile $pkg=$archive"
  done > $out/importcfg
''
