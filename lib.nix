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
  parseModKey =
    key:
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
    prefix: str: builtins.substring (builtins.stringLength prefix) (builtins.stringLength str) str;

  # Go module path case-escaping: uppercase letters become "!" + lowercase.
  # This matches the GOMODCACHE filesystem layout.
  # See: golang.org/x/mod/module.EscapePath()
  escapeModPath =
    builtins.replaceStrings
      [
        "A"
        "B"
        "C"
        "D"
        "E"
        "F"
        "G"
        "H"
        "I"
        "J"
        "K"
        "L"
        "M"
        "N"
        "O"
        "P"
        "Q"
        "R"
        "S"
        "T"
        "U"
        "V"
        "W"
        "X"
        "Y"
        "Z"
      ]
      [
        "!a"
        "!b"
        "!c"
        "!d"
        "!e"
        "!f"
        "!g"
        "!h"
        "!i"
        "!j"
        "!k"
        "!l"
        "!m"
        "!n"
        "!o"
        "!p"
        "!q"
        "!r"
        "!s"
        "!t"
        "!u"
        "!v"
        "!w"
        "!x"
        "!y"
        "!z"
      ];

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
      tags ? [], # build tags
    }:
    let
      lockfile = builtins.fromTOML (builtins.readFile goLock);
      tagFlag = if tags == [] then "" else "-tags=${builtins.concatStringsSep "," tags}";
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
          subdir = if importPath == parsed.path then "" else removePrefix "${parsed.path}/" importPath;
          srcDir = if subdir == "" then modDir else "${modDir}/${subdir}";

          # Direct dependency derivations (resolved lazily via Nix's laziness).
          deps = map (imp: packages.${imp}) (pkg.imports or [ ]);
        in
        pkgs.runCommandCC "gopkg-${sanitizeName importPath}"
          {
            nativeBuildInputs = [
              go
              go2nix
              pkgs.jq
            ];
          }
          ''
            export HOME=$NIX_BUILD_TOP
            go_os=$(go env GOOS)
            go_arch=$(go env GOARCH)

            # Discover Go source files for this package (build-constraint filtered).
            filesjson=$(go2nix list-files ${tagFlag} "${srcDir}")
            gofiles=$(echo "$filesjson" | jq -r '(.go_files // [])[]')
            cgofiles=$(echo "$filesjson" | jq -r '(.cgo_files // [])[]')
            sfiles=$(echo "$filesjson" | jq -r '(.s_files // [])[]')
            cfiles=$(echo "$filesjson" | jq -r '(.c_files // [])[]')
            hfiles=$(echo "$filesjson" | jq -r '(.h_files // [])[]')

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
              # Track compiled objects explicitly (don't use find — cgo leaves DWARF inference .o files).
              cc_files=""
              compiled_ofiles=""
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
                compiled_ofiles="$compiled_ofiles $cgowork/$base.o"
              done

              # Compile .S files (C preprocessor assembly) with gcc.
              for f in $sfiles; do
                base="$(basename "$f" .S)"
                gcc -c \
                  -I "$cgowork" \
                  -I "${srcDir}" \
                  -fPIC -pthread \
                  "$f" \
                  -o "$cgowork/$base.o"
                compiled_ofiles="$compiled_ofiles $cgowork/$base.o"
              done

              # Step 3: gcc test link + dynimport — needed for external linking.
              # Produces _cgo_import.go with //go:cgo_import_dynamic directives.
              if [ -f "$cgowork/_cgo_main.c" ]; then
                gcc -c \
                  -I "$cgowork" \
                  -I "${srcDir}" \
                  -fPIC -pthread \
                  "$cgowork/_cgo_main.c" \
                  -o "$cgowork/_cgo_main.o"
                gcc -o "$cgowork/_cgo_.o" "$cgowork/_cgo_main.o" $compiled_ofiles -lpthread || echo "note: cgo test link failed (no dynamic imports for this package)"
                if [ -f "$cgowork/_cgo_.o" ]; then
                  pkgname=$(sed -n 's/^package //p' "$cgowork/_cgo_gotypes.go" | head -1)
                  go tool cgo -dynimport "$cgowork/_cgo_.o" \
                    -dynout "$cgowork/_cgo_import.go" \
                    -dynpackage "$pkgname" \
                    -dynlinker || echo "note: cgo dynimport failed (continuing)"
                fi
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

              # Step 5: pack only our compiled C objects (not cgo DWARF inference leftovers)
              go tool pack r "$out/${importPath}.a" $compiled_ofiles

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
                  -D "GOOS_$go_os" -D "GOARCH_$go_arch" \
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
                    -D "GOOS_$go_os" -D "GOARCH_$go_arch" \
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

  # --- buildGoBinary ----------------------------------------------------------

  # buildGoBinary compiles local packages and links them into a binary.
  # Third-party packages come from mkGoPackageSet (cached per-package).
  # Local packages are compiled from src in a single derivation.
  #
  # Arguments:
  #   src         - local source tree (the Go module root)
  #   goLock      - path to go2nix.toml lockfile (default: ${src}/go2nix.toml)
  #   go          - Go toolchain
  #   go2nix      - go2nix binary (for list-files)
  #   pkgs        - nixpkgs
  #   subPackages - list of import path suffixes for main packages (default ["."])
  #   pname       - output binary name (for single-binary; multi uses baseNameOf)
  #   tags        - build tags (default [])
  #   ldflags     - linker flags (default [])
  #   CGO_ENABLED - "0" or "1" as string, or null for Go default
  buildGoBinary =
    {
      src,
      goLock ? "${src}/go2nix.toml",
      moduleDir ? ".",  # relative path from src to directory containing go.mod
      go,
      go2nix,
      pkgs,
      subPackages ? ["."],
      pname ? "go-binary",
      tags ? [],
      ldflags ? [],
      CGO_ENABLED ? null,
    }:
    let
      lockfile = builtins.fromTOML (builtins.readFile goLock);

      # Build tag flag for go tool compile and go2nix list-files.
      tagFlag = if tags == [] then "" else "-tags=${builtins.concatStringsSep "," tags}";

      # Linker flags string.
      ldflagsStr = builtins.concatStringsSep " " ldflags;

      # The main module path from go.mod (first line: "module <path>").
      # We need this to compute import paths for local packages.
      # moduleRoot is the directory containing go.mod, resolved within src.
      moduleRoot = if moduleDir == "." then "${src}" else "${src}/${moduleDir}";

      goModContent = builtins.readFile "${moduleRoot}/go.mod";
      modulePath =
        let
          lines = builtins.filter (l: l != [ ] && builtins.isString l) (builtins.split "\n" goModContent);
          moduleLine = builtins.head (
            builtins.filter (l: builtins.isString l && builtins.substring 0 7 l == "module ") lines
          );
        in
        builtins.substring 7 (builtins.stringLength moduleLine - 7) moduleLine;

      # Metadata for each sub-package to build.
      subPackageMeta = map (sp: {
        subPackage = sp;
        importPath = if sp == "." then modulePath else "${modulePath}/${sp}";
        srcDir = if sp == "." then "${src}" else "${src}/${sp}";
        binName = if sp == "." then pname else builtins.baseNameOf sp;
      }) subPackages;

      # Third-party package set.
      packageSet = mkGoPackageSet {
        inherit
          goLock
          go
          go2nix
          pkgs
          tags
          ;
      };

      stdlib = buildGoStdlib {
        inherit go;
        inherit (pkgs) runCommandCC;
      };

      # All third-party package derivations (used in importcfg for linking).
      allThirdPartyDeps = builtins.attrValues packageSet;

    in
    pkgs.runCommandCC "${pname}"
      {
        nativeBuildInputs = [
          go
          go2nix
          pkgs.jq
        ];
      }
      ''
        export HOME=$NIX_BUILD_TOP
        ${if CGO_ENABLED != null then "export CGO_ENABLED=${CGO_ENABLED}" else ""}

        go_os=$(go env GOOS)
        go_arch=$(go env GOARCH)

        # compile_go_pkg: compile a single Go package (pure Go, assembly, or cgo).
        #   $1 = p_flag (import path for libs, "main" for commands)
        #   $2 = src_dir (absolute path to package source)
        #   $3 = output_archive (path to write .a file)
        #   $4 = pkg_json (JSON string with go_files, cgo_files, s_files, etc.)
        #   $5 = unique_id (optional, for temp file uniqueness; defaults to sanitized $1)
        compile_go_pkg() {
          local p_flag="$1"
          local src_dir="$2"
          local output_archive="$3"
          local pkg_json="$4"
          local uid="''${5:-$(echo "$p_flag" | tr '/' '_')}"

          local gofiles cgofiles sfiles cfiles cxxfiles hfiles

          gofiles=$(echo "$pkg_json" | jq -r '(.go_files // [])[]')
          cgofiles=$(echo "$pkg_json" | jq -r '(.cgo_files // [])[]')
          sfiles=$(echo "$pkg_json" | jq -r '(.s_files // [])[]')
          cfiles=$(echo "$pkg_json" | jq -r '(.c_files // [])[]')
          cxxfiles=$(echo "$pkg_json" | jq -r '(.cxx_files // [])[]')
          hfiles=$(echo "$pkg_json" | jq -r '(.h_files // [])[]')

          if [ -z "$gofiles" ] && [ -z "$cgofiles" ]; then
            echo "ERROR: no Go files found in $src_dir" >&2
            echo "Package: $p_flag" >&2
            exit 1
          fi

          # Generate embedcfg if the package uses //go:embed.
          local embedflag=""
          local hasEmbed
          hasEmbed=$(echo "$pkg_json" | jq -r '.embed_cfg // empty')
          if [ -n "$hasEmbed" ]; then
            echo "$pkg_json" | jq '.embed_cfg' > "$NIX_BUILD_TOP/embedcfg_''${uid}.json"
            embedflag="-embedcfg=$NIX_BUILD_TOP/embedcfg_''${uid}.json"
          fi

          mkdir -p "$(dirname "$output_archive")"
          cd "$src_dir"

          if [ -n "$cgofiles" ]; then
            # --- Cgo compilation pipeline ---
            touch "$NIX_BUILD_TOP/.has_cgo"

            local cgowork="$NIX_BUILD_TOP/cgo_work_''${uid}"
            mkdir -p "$cgowork"

            # Copy header files so cgo/gcc can find them.
            for h in $hfiles; do
              cp "$h" "$cgowork/"
            done

            # Step 1: go tool cgo
            go tool cgo \
              -objdir "$cgowork" \
              -importpath "$p_flag" \
              -- \
              -I "$cgowork" \
              $cgofiles

            # Step 2: gcc/g++ — compile C/C++ files
            local cc_files=""
            local compiled_ofiles=""
            for f in "$cgowork"/_cgo_export.c "$cgowork"/*.cgo2.c; do
              [ -f "$f" ] && cc_files="$cc_files $f"
            done
            for f in $cfiles; do
              cc_files="$cc_files $f"
            done

            for f in $cc_files; do
              local base
              base="$(basename "$f" .c)"
              gcc -c \
                -I "$cgowork" \
                -I "$src_dir" \
                -fPIC -pthread \
                "$f" \
                -o "$cgowork/''${base}_''${uid}.o"
              compiled_ofiles="$compiled_ofiles $cgowork/''${base}_''${uid}.o"
            done

            # Compile .S files (C preprocessor assembly) with gcc.
            for f in $sfiles; do
              local base
              base="$(basename "$f" .S)"
              gcc -c \
                -I "$cgowork" \
                -I "$src_dir" \
                -fPIC -pthread \
                "$f" \
                -o "$cgowork/''${base}_asm_''${uid}.o"
              compiled_ofiles="$compiled_ofiles $cgowork/''${base}_asm_''${uid}.o"
            done

            # Compile C++ files
            for f in $cxxfiles; do
              local base
              base="$(basename "$f" .cc)"
              base="$(basename "$base" .cpp)"
              base="$(basename "$base" .cxx)"
              g++ -c \
                -I "$cgowork" \
                -I "$src_dir" \
                -fPIC -pthread \
                "$f" \
                -o "$cgowork/''${base}_cxx_''${uid}.o"
              compiled_ofiles="$compiled_ofiles $cgowork/''${base}_cxx_''${uid}.o"
            done

            # Step 3: gcc test link + dynimport
            if [ -f "$cgowork/_cgo_main.c" ]; then
              gcc -c \
                -I "$cgowork" \
                -I "$src_dir" \
                -fPIC -pthread \
                "$cgowork/_cgo_main.c" \
                -o "$cgowork/_cgo_main_''${uid}.o"
              gcc -o "$cgowork/_cgo__''${uid}.o" "$cgowork/_cgo_main_''${uid}.o" $compiled_ofiles -lpthread || echo "note: cgo test link failed (no dynamic imports for this package)"
              if [ -f "$cgowork/_cgo__''${uid}.o" ]; then
                local pkgname
                pkgname=$(sed -n 's/^package //p' "$cgowork/_cgo_gotypes.go" | head -1)
                go tool cgo -dynimport "$cgowork/_cgo__''${uid}.o" \
                  -dynout "$cgowork/_cgo_import_''${uid}.go" \
                  -dynpackage "$pkgname" \
                  -dynlinker || echo "note: cgo dynimport failed (continuing)"
              fi
            fi

            # Step 4: go tool compile — Go files + cgo-generated Go files
            local cgo_gofiles=""
            for f in "$cgowork"/_cgo_gotypes.go "$cgowork"/*.cgo1.go; do
              [ -f "$f" ] && cgo_gofiles="$cgo_gofiles $f"
            done
            [ -f "$cgowork/_cgo_import_''${uid}.go" ] && cgo_gofiles="$cgo_gofiles $cgowork/_cgo_import_''${uid}.go"

            go tool compile \
              -importcfg "$NIX_BUILD_TOP/importcfg" \
              -p "$p_flag" \
              -trimpath="$NIX_BUILD_TOP" \
              $embedflag \
              -pack \
              -o "$output_archive" \
              $gofiles $cgo_gofiles

            # Step 5: pack compiled C/C++ objects
            go tool pack r "$output_archive" $compiled_ofiles

          else
            # --- Pure Go (possibly with assembly) ---
            local compile_files="$gofiles"

            if [ -n "$sfiles" ]; then
              local asmhdr="$NIX_BUILD_TOP/go_asm_''${uid}.h"
              touch "$asmhdr"

              # First pass: generate symabis
              go tool asm \
                -p "$p_flag" \
                -trimpath "$NIX_BUILD_TOP" \
                -I "$NIX_BUILD_TOP" \
                -I "$(go env GOROOT)/pkg/include" \
                -D "GOOS_$go_os" -D "GOARCH_$go_arch" \
                -gensymabis \
                -o "$NIX_BUILD_TOP/symabis_''${uid}" \
                $sfiles

              # Compile Go with symabis + asmhdr
              go tool compile \
                -importcfg "$NIX_BUILD_TOP/importcfg" \
                -p "$p_flag" \
                -trimpath="$NIX_BUILD_TOP" \
                -symabis "$NIX_BUILD_TOP/symabis_''${uid}" \
                -asmhdr "$asmhdr" \
                $embedflag \
                -pack \
                -o "$output_archive" \
                $compile_files

              # Second pass: assemble .s files
              for sf in $sfiles; do
                local base
                base="$(basename "$sf" .s)"
                go tool asm \
                  -p "$p_flag" \
                  -trimpath "$NIX_BUILD_TOP" \
                  -I "$NIX_BUILD_TOP" \
                  -I "$(go env GOROOT)/pkg/include" \
                  -D "GOOS_$go_os" -D "GOARCH_$go_arch" \
                  -o "$NIX_BUILD_TOP/''${base}_''${uid}.o" \
                  "$sf"
                go tool pack r "$output_archive" "$NIX_BUILD_TOP/''${base}_''${uid}.o"
              done
            else
              go tool compile \
                -importcfg "$NIX_BUILD_TOP/importcfg" \
                -p "$p_flag" \
                -trimpath="$NIX_BUILD_TOP" \
                $embedflag \
                -pack \
                -o "$output_archive" \
                $compile_files
            fi
          fi
        }

        # --- Build importcfg with ALL packages (stdlib + third-party) ---
        cat "${stdlib}/importcfg" > "$NIX_BUILD_TOP/importcfg"

        ${builtins.concatStringsSep "\n" (
          map (dep: ''
            find "${dep}" -name '*.a' | while read -r pkgp; do
              relpath="''${pkgp#"${dep}/"}"
              pkgname="''${relpath%.a}"
              echo "packagefile $pkgname=$pkgp"
            done >> "$NIX_BUILD_TOP/importcfg"
          '') allThirdPartyDeps
        )}

        # --- Compile local packages (two-pass) ---
        localdir="$NIX_BUILD_TOP/local-pkgs"
        mkdir -p "$localdir"

        # Get all local packages in dependency order.
        localjson=$(go2nix list-local-packages ${tagFlag} "${moduleRoot}")

        # Pass 1: compile library packages (in topological order).
        # Use process substitution to avoid pipe subshell (which would swallow errors).
        while read -r pkgentry; do
          importpath=$(echo "$pkgentry" | jq -r '.import_path')
          srcdir=$(echo "$pkgentry" | jq -r '.src_dir')

          echo "Compiling local library: $importpath ($srcdir)"
          compile_go_pkg "$importpath" "$srcdir" "$localdir/$importpath.a" "$pkgentry"

          echo "packagefile $importpath=$localdir/$importpath.a" >> "$NIX_BUILD_TOP/importcfg"
        done < <(echo "$localjson" | jq -c '.[] | select(.is_command == false)')

        # --- Pass 2: Compile main packages and link ---
        mkdir -p "$out/bin"

        ${builtins.concatStringsSep "\n" (
          map (meta: ''
            echo "Compiling main: ${meta.importPath} (${meta.srcDir})"
            filesjson=$(go2nix list-files ${tagFlag} "${meta.srcDir}")

            compile_go_pkg "main" "${meta.srcDir}" "$localdir/${meta.importPath}.a" "$filesjson" "main_${meta.binName}"

            linkflags=""
            if [ -f "$NIX_BUILD_TOP/.has_cgo" ]; then
              linkflags="-extld gcc -linkmode external"
            fi

            go tool link \
              -importcfg "$NIX_BUILD_TOP/importcfg" \
              ${ldflagsStr} \
              $linkflags \
              -o "$out/bin/${meta.binName}" \
              "$localdir/${meta.importPath}.a"
          '') subPackageMeta
        )}
      '';

in
{
  inherit
    buildGoStdlib
    importcfgFor
    mkGoPackageSet
    buildGoBinary
    ;
}
