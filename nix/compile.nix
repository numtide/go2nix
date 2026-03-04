# go2nix/nix/compile.nix — shared Go package compilation logic.
#
# Two forms of the same compilation pipeline:
#   compileGoPackageFn   — shell function definition (for buildGoBinary, called with positional args)
#   compileGoPackageInline — Nix function returning a shell snippet with interpolated variables (for mkGoPackageSet)
#
# Both share `coreBody`: the cgo/assembly/pure-Go compilation pipeline using
# only shell variables: $p_flag, $src_dir, $output_archive, $pkg_json, $uid
{ }:
let
  # The core compilation body — uses only shell variables set by the wrapper.
  # Expects: $p_flag, $src_dir, $output_archive, $pkg_json, $uid,
  #          $go_os, $go_arch, and importcfg at $NIX_BUILD_TOP/importcfg.
  coreBody = ''
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
        # go_asm.h must be named exactly "go_asm.h" because assembly files
        # use #include "go_asm.h" and the assembler finds it via -I $NIX_BUILD_TOP.
        # Sequential execution in buildGoBinary means no clobbering risk.
        local asmhdr="$NIX_BUILD_TOP/go_asm.h"
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
  '';

  # Shell function definition for buildGoBinary.
  # Called as: compile_go_pkg <p_flag> <src_dir> <output_archive> <pkg_json> [uid]
  compileGoPackageFn = ''
    compile_go_pkg() {
      local p_flag="$1"
      local src_dir="$2"
      local output_archive="$3"
      local pkg_json="$4"
      local uid="''${5:-$(echo "$p_flag" | tr '/' '_')}"
      ${coreBody}
    }
  '';

  # Nix function returning an inline shell snippet for mkGoPackageSet.
  # Wraps coreBody in a function so `local` declarations work correctly.
  # Variables are interpolated at Nix eval time (importPath, srcDir, etc.).
  compileGoPackageInline =
    {
      importPath,
      srcDir,
    }:
    let
      uid = builtins.replaceStrings [ "/" ] [ "_" ] importPath;
    in
    ''
      __compile() {
        local p_flag="${importPath}"
        local src_dir="${srcDir}"
        local output_archive="$out/${importPath}.a"
        local pkg_json="$filesjson"
        local uid="${uid}"
        ${coreBody}
      }
      __compile
    '';

in
{
  inherit compileGoPackageFn compileGoPackageInline;
}
