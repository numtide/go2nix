# go2nix/nix/gomod2nix/default.nix — gomod2nix-style vendor + go build.
#
# Derived from go2nix/builder/default.nix on main branch, itself forked from
# github.com/nix-community/gomod2nix@47d628dc (MIT).
#
# Adapted to work as a callPackage-compatible function within the go2nix scope.
# Returns a single builder function: { src, goLock, pname, version, ... }:
#
# Expects a v1-style lockfile (generated via `go2nix generate --mode=vendor`):
#   [mod."path@version"]
#   version = "v1.2.3"
#   hash    = "sha256-..."
#   replaced = "other/path"   # optional
#
# Key differences from the original builder:
#   - `go` comes from the scope (no selectGo)
#   - `mkGoEnv` removed (handled by nix/mk-go-env.nix + nix/scope.nix)
#   - Uses `goLock` parameter name (was `modules`) for API consistency
{
  cacert,
  git,
  jq,
  lib,
  pkgsBuildBuild,
  rsync,
  runCommand,
  stdenv,
  stdenvNoCC,
  go,
  netrcFile,
}:
let
  inherit (builtins)
    elemAt
    hasAttr
    readFile
    split
    substring
    toJSON
    ;
  inherit (lib)
    concatStringsSep
    fetchers
    filterAttrs
    mapAttrs
    mapAttrsToList
    optional
    optionalAttrs
    optionalString
    removePrefix
    removeSuffix
    ;

  parseGoMod = import ./parser.nix;

  # Internal build-time Go utilities.
  internal =
    let
      mkInternalPkg =
        name: src:
        pkgsBuildBuild.runCommand "go2nix-${name}"
          {
            inherit (pkgsBuildBuild.go) GOOS GOARCH;
            nativeBuildInputs = [ pkgsBuildBuild.go ];
          }
          ''
            export HOME=$(mktemp -d)
            go build -o "$HOME/bin" ${src}
            mv "$HOME/bin" "$out"
          '';
    in
    {
      symlink = mkInternalPkg "symlink" ./symlink/symlink.go;
      install = mkInternalPkg "install" ./install/install.go;
      mvscheck = mkInternalPkg "mvscheck" ./mvscheck/mvscheck.go;
    };

  fetchGoModule =
    {
      hash,
      goPackagePath,
      version,
    }:
    stdenvNoCC.mkDerivation (
      {
        name = "${baseNameOf goPackagePath}_${version}";
        builder = ./fetch.sh;
        inherit goPackagePath version;
        nativeBuildInputs = [
          cacert
          git
          go
          jq
        ];
        outputHashMode = "recursive";
        outputHashAlgo = null;
        outputHash = hash;
        impureEnvVars = fetchers.proxyImpureEnvVars ++ [ "GOPROXY" ];
      }
      // optionalAttrs (netrcFile != null) {
        NETRC_CONTENT = readFile netrcFile;
      }
    );

  mkVendorEnv =
    {
      modulesStruct,
      goMod,
      pwd,
      defaultPackage ? "",
    }:
    let
      # Filter the shared TOML to only entries this project needs,
      # matching composite key (path@version) against go.mod requires.
      filteredMods =
        if goMod != null then
          let
            effectiveVersion =
              path:
              let
                repl = goMod.replace.${path} or null;
              in
              if repl != null && repl ? version then repl.version else goMod.require.${path};

            requiredKeys = builtins.listToAttrs (
              map (path: {
                name = "${path}@${effectiveVersion path}";
                value = true;
              }) (builtins.attrNames goMod.require)
            );

            filtered = filterAttrs (key: _: hasAttr key requiredKeys) modulesStruct.mod;

            # Tidiness check.
            localReplacePaths = builtins.attrNames (filterAttrs (_: v: v ? path) goMod.replace);
            filteredPaths = builtins.listToAttrs (
              map (key: {
                name = removeSuffix "@${filtered.${key}.version}" key;
                value = true;
              }) (builtins.attrNames filtered)
            );
            missing = builtins.filter (
              path: !(hasAttr path filteredPaths) && !(builtins.elem path localReplacePaths)
            ) (builtins.attrNames goMod.require);
          in
          if missing != [ ] then
            throw ''
              go2nix lockfile is missing required module(s):
                ${concatStringsSep "\n  " (map (p: "${p}@${effectiveVersion p}") missing)}

              Either go.mod is not tidy (require versions don't match MVS-resolved
              versions), or the lockfile is stale. Run:
                go mod tidy
                <regenerate lockfile>
            ''
          else
            filtered
        else
          modulesStruct.mod;

      # Extract bare module path from a composite TOML key.
      extractPath = key: removeSuffix "@${filteredMods.${key}.version}" key;

      rekeyToPath =
        attrs:
        builtins.listToAttrs (
          map (key: {
            name = extractPath key;
            value = attrs.${key};
          }) (builtins.attrNames attrs)
        );

      localReplaceCommands =
        let
          localReplaceAttrs = filterAttrs (_n: v: hasAttr "path" v) goMod.replace;
          commands = mapAttrsToList (name: value: ''
            mkdir -p $(dirname vendor/${name})
            ln -s ${pwd + "/${value.path}"} vendor/${name}
          '') localReplaceAttrs;
        in
        if goMod != null then commands else [ ];

      sources = mapAttrs (
        key: meta:
        let
          goPackagePath = extractPath key;
        in
        fetchGoModule {
          goPackagePath = meta.replaced or goPackagePath;
          inherit (meta) version hash;
        }
      ) filteredMods;

      excludeDefault = attrs: filterAttrs (n: _: extractPath n != defaultPackage) attrs;
    in
    runCommand "vendor-env"
      {
        nativeBuildInputs = [ go ];
        json = toJSON (rekeyToPath (excludeDefault filteredMods));
        sources = toJSON (rekeyToPath (excludeDefault sources));

        passthru = {
          sources = rekeyToPath sources;
        };

        passAsFile = [
          "json"
          "sources"
        ];
      }
      ''
        mkdir vendor

        export GOCACHE=$TMPDIR/go-cache
        export GOPATH="$TMPDIR/go"

        ${internal.symlink}
        ${concatStringsSep "\n" localReplaceCommands}

        mv vendor $out
      '';

  stripVersion =
    version:
    let
      parts = elemAt (split "(\\+|-)" (removePrefix "v" version));
      v = parts 0;
      d = parts 2;
    in
    if v != "0.0.0" then
      v
    else
      "unstable-"
      + (concatStringsSep "-" [
        (substring 0 4 d)
        (substring 4 2 d)
        (substring 6 2 d)
      ]);

in

# Builder function: { src, goLock, pname, version, ... } -> derivation
{
  src,
  goLock,
  pname ? null,
  version ? null,
  pwd ? null,
  nativeBuildInputs ? [ ],
  allowGoReference ? false,
  meta ? { },
  passthru ? { },
  tags ? [ ],
  CGO_ENABLED ? null,
  goModFile ? null,
  subPackages ? null,
  ...
}@args:
let
  modulesStruct = if goLock == null then { } else fromTOML (readFile goLock);

  effectivePwd = if pwd != null then pwd else src;

  goModPath = if goModFile != null then goModFile else "${toString effectivePwd}/go.mod";

  goMod = if effectivePwd != null || goModFile != null then parseGoMod (readFile goModPath) else null;

  defaultPackage = modulesStruct.goPackagePath or "";

  findMod =
    modPath:
    let
      matching = filterAttrs (k: _: lib.hasPrefix (modPath + "@") k) modulesStruct.mod;
    in
    builtins.head (builtins.attrValues matching);

  vendorEnv = mkVendorEnv {
    inherit
      defaultPackage
      goMod
      modulesStruct
      ;
    pwd = effectivePwd;
  };

  effectivePname = if pname != null then pname else baseNameOf defaultPackage;

  effectiveVersion =
    if version != null then
      version
    else if defaultPackage != "" then
      stripVersion (findMod defaultPackage).version
    else
      "0.0.0";

  extraArgs = builtins.removeAttrs args [
    "src"
    "goLock"
    "pname"
    "version"
    "pwd"
    "nativeBuildInputs"
    "allowGoReference"
    "meta"
    "passthru"
    "tags"
    "ldflags"
    "CGO_ENABLED"
    "goModFile"
    "subPackages"
  ];

in
stdenv.mkDerivation (
  optionalAttrs (defaultPackage != "") {
    src = vendorEnv.passthru.sources.${defaultPackage};
  }
  // optionalAttrs (subPackages == null && hasAttr "subPackages" modulesStruct) {
    inherit (modulesStruct) subPackages;
  }
  // extraArgs
  // {
    pname = effectivePname;
    version = effectiveVersion;
    inherit src meta;

    nativeBuildInputs = [
      rsync
      go
    ]
    ++ nativeBuildInputs;

    inherit (go) GOOS GOARCH;

    GO_NO_VENDOR_CHECKS = "1";
    CGO_ENABLED = if CGO_ENABLED != null then CGO_ENABLED else go.CGO_ENABLED;

    GO111MODULE = "on";
    GOFLAGS = [ "-mod=vendor" ] ++ lib.optionals (!allowGoReference) [ "-trimpath" ];

    configurePhase =
      args.configurePhase or ''
        runHook preConfigure

        export GOCACHE=$TMPDIR/go-cache
        export GOPATH="$TMPDIR/go"
        export GOSUMDB=off
        export GOPROXY=off
        cd "''${modRoot:-.}"

        ${optionalString (modulesStruct != { }) ''
          if [ -n "${vendorEnv}" ]; then
            rm -rf vendor
            rsync -a -K --ignore-errors ${vendorEnv}/ vendor

            ${internal.mvscheck}
          fi
        ''}

        runHook postConfigure
      '';

    buildPhase =
      args.buildPhase or ''
        runHook preBuild

        exclude='\(/_\|examples\|Godeps\|testdata'
        if [[ -n "$excludedPackages" ]]; then
          IFS=' ' read -r -a excludedArr <<<$excludedPackages
          printf -v excludedAlternates '%s\\|' "''${excludedArr[@]}"
          excludedAlternates=''${excludedAlternates%\\|} # drop final \| added by printf
          exclude+='\|'"$excludedAlternates"
        fi
        exclude+='\)'

        buildGoDir() {
          local cmd="$1" dir="$2"

          . $TMPDIR/buildFlagsArray

          declare -a flags
          flags+=($buildFlags "''${buildFlagsArray[@]}")
          flags+=(''${tags:+-tags=${lib.concatStringsSep "," tags}})
          flags+=(''${ldflags:+-ldflags="$ldflags"})
          flags+=("-v" "-p" "$NIX_BUILD_CORES")

          if [ "$cmd" = "test" ]; then
            flags+=(-vet=off)
            flags+=($checkFlags)
          fi

          local OUT
          if ! OUT="$(go $cmd "''${flags[@]}" $dir 2>&1)"; then
            if echo "$OUT" | grep -qE 'imports .*?: no Go files in'; then
              echo "$OUT" >&2
              return 1
            fi
            if ! echo "$OUT" | grep -qE '(no( buildable| non-test)?|build constraints exclude all) Go (source )?files'; then
              echo "$OUT" >&2
              return 1
            fi
          fi
          if [ -n "$OUT" ]; then
            echo "$OUT" >&2
          fi
          return 0
        }

        getGoDirs() {
          local type;
          type="$1"
          if [ -n "$subPackages" ]; then
            echo "$subPackages" | sed "s,\(^\| \),\1./,g"
          else
            find . -type f -name \*$type.go -exec dirname {} \; | grep -v "/vendor/" | sort --unique | grep -v "$exclude"
          fi
        }

        if (( "''${NIX_DEBUG:-0}" >= 1 )); then
          buildFlagsArray+=(-x)
        fi

        if [ ''${#buildFlagsArray[@]} -ne 0 ]; then
          declare -p buildFlagsArray > $TMPDIR/buildFlagsArray
        else
          touch $TMPDIR/buildFlagsArray
        fi
        if [ -z "$enableParallelBuilding" ]; then
            export NIX_BUILD_CORES=1
        fi
        for pkg in $(getGoDirs ""); do
          echo "Building subPackage $pkg"
          buildGoDir install "$pkg"
        done
      ''
      + optionalString (stdenv.hostPlatform != stdenv.buildPlatform) ''
        # normalize cross-compiled builds w.r.t. native builds
        (
          dir=$GOPATH/bin/${go.GOOS}_${go.GOARCH}
          if [[ -n "$(shopt -s nullglob; echo $dir/*)" ]]; then
            mv $dir/* $dir/..
          fi
          if [[ -d $dir ]]; then
            rmdir $dir
          fi
        )
      ''
      + ''
        runHook postBuild
      '';

    doCheck = args.doCheck or true;
    checkPhase =
      args.checkPhase or ''
        runHook preCheck

        # We do not set trimpath for tests, in case they reference test assets
        export GOFLAGS=''${GOFLAGS//-trimpath/}

        for pkg in $(getGoDirs test); do
          buildGoDir test "$pkg"
        done

        runHook postCheck
      '';

    installPhase =
      args.installPhase or ''
        runHook preInstall

        mkdir -p $out
        dir="$GOPATH/bin"
        [ -e "$dir" ] && cp -r $dir $out

        runHook postInstall
      '';

    strictDeps = true;

    disallowedReferences = optional (!allowGoReference) go;

    passthru = {
      inherit go vendorEnv goLock;
    }
    // passthru;
  }
)
