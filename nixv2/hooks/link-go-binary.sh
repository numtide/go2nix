# Atomic hook: compile local packages and link Go binaries.
#
# Expected environment variables (set via derivation `env`):
#   goModuleRoot    — absolute path to module root (containing go.mod)
#   goModulePath    — Go module path (from go.mod)
#   goSubPackages   — space-separated list of sub-packages (default: ".")
#   goLdflags       — extra linker flags (optional)
#   goPname         — binary name for "." package (optional)
#
# Third-party dependencies are discovered from $buildInputs at build time.

linkGoBinaryConfigurePhase() {
    runHook preConfigure

    # Build importcfg: stdlib + all third-party deps.
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

    runHook postConfigure
}

linkGoBinaryBuildPhase() {
    runHook preBuild

    local localdir="$NIX_BUILD_TOP/local-pkgs"
    mkdir -p "$localdir"

    # Pass 1: compile library packages in parallel (DAG-aware).
    @go2nix@ compile-module \
        --importcfg "$NIX_BUILD_TOP/importcfg" \
        --outdir "$localdir" \
        --trimpath "$NIX_BUILD_TOP" \
        @tagArg@ \
        "$goModuleRoot"

    # Pass 2: compile main packages and link.
    mkdir -p "$NIX_BUILD_TOP/staging/bin"

    for sp in ${goSubPackages:-.}; do
        local importpath srcdir binname

        if [ "$sp" = "." ]; then
            importpath="$goModulePath"
            srcdir="$goModuleRoot"
            binname="${goPname:-$(basename "$goModulePath")}"
        else
            importpath="$goModulePath/$sp"
            srcdir="$goModuleRoot/$sp"
            binname="$(basename "$sp")"
        fi

        echo "Compiling main: $importpath"
        @go2nix@ compile-package \
            --importcfg "$NIX_BUILD_TOP/importcfg" \
            --import-path "main" \
            --src-dir "$srcdir" \
            --output "$localdir/$importpath.a" \
            --trimpath "$NIX_BUILD_TOP" \
            @tagArg@

        local linkflags=""
        if [ -f "$NIX_BUILD_TOP/.has_cgo" ]; then
            linkflags="-extld $CC -linkmode external"
        fi

        @go@ tool link \
            -buildid=redacted \
            -importcfg "$NIX_BUILD_TOP/importcfg" \
            ${goLdflags:-} \
            $linkflags \
            -o "$NIX_BUILD_TOP/staging/bin/$binname" \
            "$localdir/$importpath.a"
    done

    runHook postBuild
}

linkGoBinaryInstallPhase() {
    runHook preInstall

    mkdir -p "$out/bin"
    cp "$NIX_BUILD_TOP/staging/bin/"* "$out/bin/"

    runHook postInstall
}

configurePhase=linkGoBinaryConfigurePhase
buildPhase=linkGoBinaryBuildPhase
installPhase=linkGoBinaryInstallPhase
dontUnpack=1
dontFixup=1
