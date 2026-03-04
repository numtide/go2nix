# go2nix/lib.nix — package-level Go build system.
#
# Three main entry points:
#   buildGoStdlib { go, runCommandCC }: compiles Go stdlib, exports .a files + importcfg
#   importcfgFor { stdlib, deps }: shell snippet to build importcfg from deps
#   mkGoPackageSet { goLock, go, go2nix, pkgs }: per-package derivations from a lockfile
{ }:
let
  # buildGoStdlib compiles the entire Go standard library and produces:
  #   $out/<pkg>.a     for each stdlib package
  #   $out/importcfg   mapping import paths to .a file paths
  #
  # This is a single derivation shared by all builds using the same Go version.
  # Reference: TVL depot nix/buildGo/default.nix buildStdlib.
  buildGoStdlib =
    {
      go,
      runCommandCC,
    }:
    runCommandCC "go-stdlib-${go.version}" { nativeBuildInputs = [ go ]; } ''
      HOME=$NIX_BUILD_TOP/home
      mkdir -p $HOME

      # Copy Go source tree so we can write to it.
      goroot="$(go env GOROOT)"
      cp -R "$goroot/src" "$goroot/pkg" .
      chmod -R +w .

      # Compile all stdlib packages.
      GODEBUG=installgoroot=all GOROOT=$NIX_BUILD_TOP go install -v --trimpath std 2>&1

      # Collect .a files into $out and generate importcfg.
      mkdir -p $out
      cp -r pkg/*_*/* $out

      find $out -name '*.a' | sort | while read -r archive; do
        rel="''${archive#"$out/"}"
        pkg="''${rel%.a}"
        echo "packagefile $pkg=$archive"
      done > $out/importcfg
    '';

  # importcfgFor generates shell commands that build an importcfg file
  # starting from the stdlib importcfg and adding entries from dep derivations.
  # Each dep is expected to have $out/<importpath>.a
  importcfgFor =
    {
      stdlib,
      deps, # list of package derivations
    }:
    ''
      cat "${stdlib}/importcfg" > importcfg
      ${builtins.concatStringsSep "\n" (
        map (dep: ''
          find "${dep}" -name '*.a' | while read -r pkgp; do
            relpath="''${pkgp#"${dep}/"}"
            pkgname="''${relpath%.a}"
            echo "packagefile $pkgname=$pkgp"
          done >> importcfg
        '') deps
      )}
    '';

  # --- Helpers ----------------------------------------------------------------

  # Parse a module key like "github.com/foo/bar@v1.2.3" into { path, version }.
  parseModKey = key:
    let
      parts = builtins.split "@" key;
    in
    {
      path = builtins.elemAt parts 0;
      version = builtins.elemAt parts 2;
    };

  # Make a string safe for use as a Nix derivation name.
  sanitizeName = builtins.replaceStrings [ "/" "+" ] [ "-" "_" ];

  # Remove a prefix from a string. Assumes prefix is actually a prefix.
  removePrefix =
    prefix: str:
    builtins.substring (builtins.stringLength prefix) (builtins.stringLength str) str;

  # Go module path case-escaping: uppercase letters become "!" + lowercase.
  # This matches the GOMODCACHE filesystem layout.
  # See: golang.org/x/mod/module.EscapePath()
  escapeModPath = builtins.replaceStrings
    [ "A" "B" "C" "D" "E" "F" "G" "H" "I" "J" "K" "L" "M" "N" "O" "P" "Q" "R" "S" "T" "U" "V" "W" "X" "Y" "Z" ]
    [ "!a" "!b" "!c" "!d" "!e" "!f" "!g" "!h" "!i" "!j" "!k" "!l" "!m" "!n" "!o" "!p" "!q" "!r" "!s" "!t" "!u" "!v" "!w" "!x" "!y" "!z" ];

  # --- mkGoPackageSet ---------------------------------------------------------

  # mkGoPackageSet reads a go2nix lockfile and produces one derivation per
  # third-party Go package. Each derivation compiles a single package via
  # `go tool compile` and outputs $out/<importpath>.a.
  #
  # Returns an attrset: { "import/path" = <derivation>; ... }
  mkGoPackageSet =
    {
      goLock, # path to go2nix.toml lockfile
      go, # Go toolchain
      go2nix, # go2nix binary (for list-files subcommand)
      pkgs, # nixpkgs
    }:
    let
      lockfile = builtins.fromTOML (builtins.readFile goLock);
      stdlib = buildGoStdlib {
        inherit go;
        inherit (pkgs) runCommandCC;
      };

      # --- Module fetching (FODs) ---

      # fetchModuleProxy downloads a module via the Go module proxy and
      # produces the GOMODCACHE directory structure as output.
      fetchModule =
        modKey: mod:
        let
          parsed = parseModKey modKey;
          fetchPath = if mod ? replaced then mod.replaced else parsed.path;
        in
        pkgs.stdenvNoCC.mkDerivation {
          name = "gomod-${sanitizeName modKey}";

          # Fixed-output derivation: content-addressed by NAR hash.
          outputHashAlgo = "sha256";
          outputHashMode = "recursive";
          outputHash = mod.hash;

          nativeBuildInputs = [
            go
            pkgs.cacert
          ];

          # No source — we download in the build phase.
          dontUnpack = true;

          buildPhase = ''
            export HOME=$TMPDIR
            export GOMODCACHE=$out
            export GONOSUMDB='*'
            export GONOSUMCHECK='*'
            go mod download "${fetchPath}@${mod.version}"
          '';

          # Skip other phases.
          dontInstall = true;
          dontFixup = true;
        };

      # One FOD per module.
      moduleSrcs = builtins.mapAttrs fetchModule lockfile.mod;

      # --- Package compilation ---

      # buildPackage compiles a single Go package.
      buildPackage =
        importPath: pkg:
        let
          modKey = pkg.module;
          mod = lockfile.mod.${modKey};
          modSrc = moduleSrcs.${modKey};
          parsed = parseModKey modKey;

          # The actual path in GOMODCACHE where source files live.
          # GOMODCACHE uses case-escaped paths (uppercase → !lowercase).
          fetchPath = if mod ? replaced then mod.replaced else parsed.path;
          modDir = "${modSrc}/${escapeModPath fetchPath}@${parsed.version}";

          # Subdirectory within the module for this specific package.
          # e.g., import "golang.org/x/net/http2" in module "golang.org/x/net"
          #        → subdir = "http2"
          subdir =
            if importPath == parsed.path then
              ""
            else
              removePrefix "${parsed.path}/" importPath;
          srcDir = if subdir == "" then modDir else "${modDir}/${subdir}";

          # Direct dependency derivations (resolved lazily via Nix's laziness).
          deps = map (imp: packages.${imp}) (pkg.imports or [ ]);
        in
        pkgs.runCommandCC "gopkg-${sanitizeName importPath}" {
          nativeBuildInputs = [
            go
            go2nix
            pkgs.jq
          ];
        } ''
          export HOME=$NIX_BUILD_TOP

          # Discover Go source files for this package (build-constraint filtered).
          filesjson=$(go2nix list-files "${srcDir}")
          gofiles=$(echo "$filesjson" | jq -r '.go_files[]')
          cgofiles=$(echo "$filesjson" | jq -r '.cgo_files[]')
          sfiles=$(echo "$filesjson" | jq -r '.s_files[]')
          cfiles=$(echo "$filesjson" | jq -r '.c_files[]')
          hfiles=$(echo "$filesjson" | jq -r '.h_files[]')

          if [ -z "$gofiles" ] && [ -z "$cgofiles" ]; then
            echo "ERROR: no Go files found in ${srcDir}" >&2
            echo "Package: ${importPath}" >&2
            exit 1
          fi

          # Generate embedcfg if the package uses //go:embed.
          embedflag=""
          hasEmbed=$(echo "$filesjson" | jq -r '.embed_cfg // empty')
          if [ -n "$hasEmbed" ]; then
            echo "$filesjson" | jq '.embed_cfg' > "$NIX_BUILD_TOP/embedcfg.json"
            embedflag="-embedcfg=$NIX_BUILD_TOP/embedcfg.json"
          fi

          # Build importcfg: stdlib + direct dependencies.
          ${importcfgFor { inherit stdlib deps; }}

          # Create output directory.
          mkdir -p "$out/$(dirname "${importPath}")"
          cd "${srcDir}"

          if [ -n "$cgofiles" ]; then
            # --- Cgo compilation pipeline ---
            cgowork="$NIX_BUILD_TOP/cgo_work"
            mkdir -p "$cgowork"

            # Copy header files so cgo/gcc can find them.
            for h in $hfiles; do
              cp "$h" "$cgowork/"
            done

            # Step 1: go tool cgo — generates _cgo_gotypes.go, *.cgo1.go, *.cgo2.c, _cgo_export.{c,h}
            go tool cgo \
              -objdir "$cgowork" \
              -importpath "${importPath}" \
              -- \
              -I "$cgowork" \
              $cgofiles

            # Step 2: gcc — compile C files (_cgo_export.c, *.cgo2.c, plus any .c source files)
            cc_files=""
            for f in "$cgowork"/_cgo_export.c "$cgowork"/*.cgo2.c; do
              [ -f "$f" ] && cc_files="$cc_files $f"
            done
            for f in $cfiles; do
              cc_files="$cc_files $f"
            done

            for f in $cc_files; do
              base="$(basename "$f" .c)"
              gcc -c \
                -I "$cgowork" \
                -I "${srcDir}" \
                -fPIC -pthread \
                "$f" \
                -o "$cgowork/$base.o"
            done

            # Step 3: gcc test link + dynimport — needed for external linking.
            # Produces _cgo_import.go with //go:cgo_import_dynamic directives.
            ofiles=$(find "$cgowork" -name '*.o' -not -name '_cgo_main.o' | sort)
            gcc -o "$cgowork/_cgo_.o" "$cgowork/_cgo_main.o" $ofiles -lpthread 2>/dev/null || true
            if [ -f "$cgowork/_cgo_.o" ]; then
              # Extract the package name from the cgo-generated file (always correct).
              pkgname=$(sed -n 's/^package //p' "$cgowork/_cgo_gotypes.go" | head -1)
              go tool cgo -dynimport "$cgowork/_cgo_.o" \
                -dynout "$cgowork/_cgo_import.go" \
                -dynpackage "$pkgname" \
                -dynlinker 2>/dev/null || true
            fi

            # Step 4: go tool compile — compile Go files + cgo-generated Go files
            cgo_gofiles=""
            for f in "$cgowork"/_cgo_gotypes.go "$cgowork"/*.cgo1.go; do
              [ -f "$f" ] && cgo_gofiles="$cgo_gofiles $f"
            done
            [ -f "$cgowork/_cgo_import.go" ] && cgo_gofiles="$cgo_gofiles $cgowork/_cgo_import.go"

            go tool compile \
              -importcfg "$NIX_BUILD_TOP/importcfg" \
              -p "${importPath}" \
              -trimpath="$NIX_BUILD_TOP" \
              $embedflag \
              -pack \
              -o "$out/${importPath}.a" \
              $gofiles $cgo_gofiles

            # Step 5: pack C objects into the archive
            go tool pack r "$out/${importPath}.a" $ofiles

          else
            # --- Pure Go compilation ---
            compile_files="$gofiles"

            # Handle assembly files (.s)
            if [ -n "$sfiles" ]; then
              asmhdr="$NIX_BUILD_TOP/go_asm.h"
              # Create blank go_asm.h for gensymabis pass (real one generated by compile).
              touch "$asmhdr"
              # First pass: generate symabis
              go tool asm \
                -p "${importPath}" \
                -trimpath "$NIX_BUILD_TOP" \
                -I "$NIX_BUILD_TOP" \
                -I "$(go env GOROOT)/pkg/include" \
                -D GOOS_linux -D GOARCH_amd64 \
                -gensymabis \
                -o "$NIX_BUILD_TOP/symabis" \
                $sfiles

              # Compile Go with symabis + asmhdr
              go tool compile \
                -importcfg "$NIX_BUILD_TOP/importcfg" \
                -p "${importPath}" \
                -trimpath="$NIX_BUILD_TOP" \
                -symabis "$NIX_BUILD_TOP/symabis" \
                -asmhdr "$asmhdr" \
                $embedflag \
                -pack \
                -o "$out/${importPath}.a" \
                $compile_files

              # Second pass: assemble .s files
              for sf in $sfiles; do
                base="$(basename "$sf" .s)"
                go tool asm \
                  -p "${importPath}" \
                  -trimpath "$NIX_BUILD_TOP" \
                  -I "$NIX_BUILD_TOP" \
                  -I "$(go env GOROOT)/pkg/include" \
                  -D GOOS_linux -D GOARCH_amd64 \
                  -o "$NIX_BUILD_TOP/$base.o" \
                  "$sf"
                go tool pack r "$out/${importPath}.a" "$NIX_BUILD_TOP/$base.o"
              done
            else
              go tool compile \
                -importcfg "$NIX_BUILD_TOP/importcfg" \
                -p "${importPath}" \
                -trimpath="$NIX_BUILD_TOP" \
                $embedflag \
                -pack \
                -o "$out/${importPath}.a" \
                $compile_files
            fi
          fi
        '';

      # One derivation per package, wired by lazy attrset self-reference.
      packages = builtins.mapAttrs buildPackage lockfile.pkg;

    in
    packages;

in
{
  inherit
    buildGoStdlib
    importcfgFor
    mkGoPackageSet
    ;
}
