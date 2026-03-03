# go2nix builder: buildGoApplication + mkGoEnv using a composite-key lockfile.
#
# Derived from github.com/nix-community/gomod2nix@47d628dc (MIT).
# Changes — see ./README.md:
#   - netrcFile for private-module authentication (see upstream PR #243)
#   - goModFile to avoid Import-From-Derivation (see upstream PR #243)
#   - Composite module@version keys: lockfile filtered against go.mod at eval,
#     so one lockfile can serve N projects and staleness is a build failure.
#   - Removed: build hooks, mkGoCacheEnv/cachegen, updateScript (upstream
#     features our use-case doesn't need; could be re-added if you want them).

{
  buildPackages,
  cacert,
  git,
  jq,
  lib,
  pkgsBuildBuild,
  rsync,
  runCommand,
  stdenv,
  stdenvNoCC,
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
    pathExists
    removePrefix
    removeSuffix
    ;

  parseGoMod = import ./parser.nix;

  # Internal only build-time attributes
  internal =
    let
      mkInternalPkg =
        name: src:
        pkgsBuildBuild.runCommand "gomod2nix-${name}"
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
      # Create a symlink tree of vendored sources
      symlink = mkInternalPkg "symlink" ./symlink/symlink.go;

      # Install development dependencies from tools.go
      install = mkInternalPkg "install" ./install/install.go;

      # Verify the vendor tree satisfies MVS (catches untidy go.mod at build
      # time, including the "wrong version from another project" gap that
      # eval-time checks can't see).
      mvscheck = mkInternalPkg "mvscheck" ./mvscheck/mvscheck.go;
    };

  fetchGoModule =
    {
      hash,
      goPackagePath,
      version,
      go,
      netrcFile ? null,
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
        # GOPROXY: Go module proxy URL (standard Go env var)
        impureEnvVars = fetchers.proxyImpureEnvVars ++ [ "GOPROXY" ];
      }
      # netrcFile: Path to .netrc file for private module authentication
      # Read at eval time and passed as env var
      // optionalAttrs (netrcFile != null) {
        NETRC_CONTENT = readFile netrcFile;
      }
    );

  mkVendorEnv =
    {
      go,
      modulesStruct,
      localReplaceCommands ? [ ],
      defaultPackage ? "",
      goMod,
      pwd,
      netrcFile ? null,
    }:
    let
      # Filter the shared TOML to only entries this project needs,
      # matching composite key (path@version) against go.mod requires.
      #
      # INVARIANT: this relies on go.mod being *tidy*. The lockfile records
      # MVS-resolved versions (what `go mod download` outputs), while
      # goMod.require gives the literal versions from go.mod. These match
      # iff go.mod is tidy. We check below and throw on mismatch.
      filteredMods =
        if goMod != null then
          let
            # For a remotely replaced module, the effective version is the
            # replace's version (what the CLI recorded). Local replaces have
            # .path, not .version — they aren't in the lockfile at all.
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

            # Tidiness check: every required non-local-replace module must be in
            # the filtered set. If not, either go.mod is untidy (require version
            # != MVS-resolved version) or the lockfile is stale. Both are errors
            # we'd rather surface at eval than produce an incomplete vendor tree.
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
      # "github.com/foo@v1.2.3" -> "github.com/foo"
      extractPath = key: removeSuffix "@${filteredMods.${key}.version}" key;

      # Re-key a map from composite keys to bare module paths for symlink.go.
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
          localReplaceAttrs = filterAttrs (n: v: hasAttr "path" v) goMod.replace;
          commands = (
            mapAttrsToList (name: value: (''
              mkdir -p $(dirname vendor/${name})
              ln -s ${pwd + "/${value.path}"} vendor/${name}
            '')) localReplaceAttrs
          );
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
          inherit go netrcFile;
        }
      ) filteredMods;

      # Exclude defaultPackage using extracted path.
      excludeDefault = attrs: filterAttrs (n: _: extractPath n != defaultPackage) attrs;
    in
    runCommand "vendor-env"
      {
        nativeBuildInputs = [ go ];
        json = toJSON (rekeyToPath (excludeDefault filteredMods));

        sources = toJSON (rekeyToPath (excludeDefault sources));

        passthru = {
          # Re-key to bare paths so callers can look up by module path.
          sources = rekeyToPath sources;
        };

        passAsFile = [
          "json"
          "sources"
        ];
      }
      (''
        mkdir vendor

        export GOCACHE=$TMPDIR/go-cache
        export GOPATH="$TMPDIR/go"

        ${internal.symlink}
        ${concatStringsSep "\n" localReplaceCommands}

        mv vendor $out
      '');

  # Return a Go attribute and error out if the Go version is older than was specified in go.mod.
  selectGo =
    attrs: goMod:
    attrs.go or (
      if goMod == null then
        buildPackages.go
      else
        (
          let
            goVersion = goMod.go;
            goAttrs = lib.reverseList (
              builtins.filter (
                attr:
                lib.hasPrefix "go_" attr
                && (
                  let
                    try = builtins.tryEval buildPackages.${attr};
                  in
                  try.success && try.value ? version
                )
                && lib.versionAtLeast buildPackages.${attr}.version goVersion
              ) (lib.attrNames buildPackages)
            );
            goAttr = elemAt goAttrs 0;
          in
          (
            if goAttrs != [ ] then
              buildPackages.${goAttr}
            else
              throw "go.mod specified Go version ${goVersion}, but no compatible Go attribute could be found."
          )
        )
    );

  # Strip extra data that Go adds to versions, and fall back to a version based on the date if it's a placeholder value.
  # This is data that Nix can't handle in the version attribute.
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

  mkGoEnv =
    {
      pwd,
      toolsGo ? pwd + "/tools.go",
      modules ? pwd + "/go2nix.toml",
      ...
    }@attrs:
    let
      goMod = parseGoMod (readFile "${toString pwd}/go.mod");
      modulesStruct = fromTOML (readFile modules);

      go = selectGo attrs goMod;

      vendorEnv = mkVendorEnv {
        inherit
          go
          goMod
          modulesStruct
          pwd
          ;
      };

    in
    stdenv.mkDerivation (
      removeAttrs attrs [ "pwd" ]
      // {
        name = "${baseNameOf goMod.module}-env";

        dontUnpack = true;
        dontConfigure = true;
        dontInstall = true;

        CGO_ENABLED = attrs.CGO_ENABLED or go.CGO_ENABLED;

        nativeBuildInputs = [ rsync ];

        propagatedBuildInputs = [ go ];

        GO_NO_VENDOR_CHECKS = "1";

        GO111MODULE = "on";
        GOFLAGS = "-mod=vendor";

        preferLocalBuild = true;

        buildPhase = ''
          mkdir $out

          export GOCACHE=$TMPDIR/go-cache
          export GOPATH="$out"
          export GOSUMDB=off
          export GOPROXY=off

        ''
        + optionalString (pathExists toolsGo) ''
          mkdir source
          cp ${pwd + "/go.mod"} source/go.mod
          cp ${pwd + "/go.sum"} source/go.sum
          cp ${toolsGo} source/tools.go
          cd source

          rsync -a -K --ignore-errors ${vendorEnv}/ vendor

          ${internal.install}
        '';
      }
    );

  buildGoApplication =
    {
      modules ? pwd + "/go2nix.toml",
      src ? pwd,
      pwd ? null,
      nativeBuildInputs ? [ ],
      allowGoReference ? false,
      meta ? { },
      passthru ? { },
      tags ? [ ],
      ldflags ? [ ],
      # Path to .netrc file for private module authentication
      netrcFile ? null,
      # Optional: path to go.mod file for parsing (avoids IFD when pwd is a derivation)
      # Use this when pwd points to a derivation output to avoid eval-time IFD
      goModFile ? null,

      ...
    }@attrs:
    let
      modulesStruct = if modules == null then { } else fromTOML (readFile modules);

      # Use explicit goModFile if provided, otherwise derive from pwd
      goModPath = if goModFile != null then goModFile else "${toString pwd}/go.mod";

      # Don't use pathExists on derivation outputs as it forces IFD (Import From Derivation).
      # If pwd is provided, assume go.mod exists there.
      goMod = if pwd != null || goModFile != null then parseGoMod (readFile goModPath) else null;

      go = selectGo attrs goMod;

      defaultPackage = modulesStruct.goPackagePath or "";

      # Find a module entry by bare path from composite keys.
      # Only used when defaultPackage is set (none of our projects set it).
      findMod =
        modPath:
        let
          matching = filterAttrs (k: _: lib.hasPrefix (modPath + "@") k) modulesStruct.mod;
        in
        builtins.head (builtins.attrValues matching);

      vendorEnv = mkVendorEnv {
        inherit
          defaultPackage
          go
          goMod
          modulesStruct
          netrcFile
          pwd
          ;
      };

      pname = attrs.pname or baseNameOf defaultPackage;

    in
    stdenv.mkDerivation (
      optionalAttrs (defaultPackage != "") {
        inherit pname;
        version = stripVersion (findMod defaultPackage).version;
        src = vendorEnv.passthru.sources.${defaultPackage};
      }
      // optionalAttrs (hasAttr "subPackages" modulesStruct) {
        subPackages = modulesStruct.subPackages;
      }
      // attrs
      // {
        nativeBuildInputs = [
          rsync
          go
        ]
        ++ nativeBuildInputs;

        inherit (go) GOOS GOARCH;

        GO_NO_VENDOR_CHECKS = "1";
        CGO_ENABLED = attrs.CGO_ENABLED or go.CGO_ENABLED;

        GO111MODULE = "on";
        GOFLAGS = [ "-mod=vendor" ] ++ lib.optionals (!allowGoReference) [ "-trimpath" ];

        configurePhase =
          attrs.configurePhase or ''
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

                # Build-time MVS check: walk the vendored modules' go.mod files
                # and verify every transitive require is satisfied by go.mod's
                # selected versions. This is the only check that catches an
                # untidy go.mod when the stale version happens to exist in the
                # shared lockfile (put there by another project).
                ${internal.mvscheck}
              fi
            ''}

            runHook postConfigure
          '';

        buildPhase =
          attrs.buildPhase or ''
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

        doCheck = attrs.doCheck or true;
        checkPhase =
          attrs.checkPhase or ''
            runHook preCheck

            # We do not set trimpath for tests, in case they reference test assets
            export GOFLAGS=''${GOFLAGS//-trimpath/}

            for pkg in $(getGoDirs test); do
              buildGoDir test "$pkg"
            done

            runHook postCheck
          '';

        installPhase =
          attrs.installPhase or ''
            runHook preInstall

            mkdir -p $out
            dir="$GOPATH/bin"
            [ -e "$dir" ] && cp -r $dir $out

            runHook postInstall
          '';

        strictDeps = true;

        disallowedReferences = optional (!allowGoReference) go;

        passthru = {
          inherit go vendorEnv;
        }
        // passthru;

        inherit meta;
      }
    );

in
{
  inherit buildGoApplication mkGoEnv;
}
