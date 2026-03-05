# Atomic hook: compile a single Go package via go2nix compile-package.
#
# Expected environment variables (set via derivation `env`):
#   goPackagePath   — import path of the package to compile
#   goPackageSrcDir — absolute path to the source directory
#
# Dependencies are discovered from $buildInputs at build time.

compileGoPkgBuildPhase() {
    runHook preBuild

    # Build importcfg: stdlib + dependency .a files.
    cat "@stdlib@/importcfg" > "$NIX_BUILD_TOP/importcfg"
    for dep in $buildInputs; do
        if [ -d "$dep" ]; then
            find "$dep" -name '*.a' 2>/dev/null | while read -r archive; do
                rel="''${archive#"$dep/"}"
                pkg="''${rel%.a}"
                echo "packagefile $pkg=$archive"
            done >> "$NIX_BUILD_TOP/importcfg"
        fi
    done

    # Compile the package.
    mkdir -p "$out/$(dirname "$goPackagePath")"

    @go2nix@ compile-package \
        --importcfg "$NIX_BUILD_TOP/importcfg" \
        --import-path "$goPackagePath" \
        --src-dir "$goPackageSrcDir" \
        --output "$out/$goPackagePath.a" \
        --trimpath "$NIX_BUILD_TOP" \
        @tagArg@

    runHook postBuild
}

buildPhase=compileGoPkgBuildPhase
dontUnpack=1
dontInstall=1
dontFixup=1
