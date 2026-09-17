# Builder API Reference

Both builders accept a shared set of attributes. Differences are noted below.

## `buildGoApplication` (default mode)

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
}
```

## `buildGoApplicationExperimental` (experimental mode)

```nix
goEnv.buildGoApplicationExperimental {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
}
```

Requires `nixPackage` to be set in `mkGoEnv` and Nix >= 2.34 with
`recursive-nix`, `ca-derivations`, and `dynamic-derivations` enabled.

The result is a wrapper derivation whose output is a `.drv` file; the binary
is its `.target` attribute (see [Experimental Mode](modes/experimental-mode.md)).
Attributes marked "default only" below are not rejected by this builder, they
are silently ignored.

## Required attributes

### `src` {#src}

Source tree. For monorepos with `modRoot`, this should be the repository
root.

In default mode, `src` is passed to `builtins.resolveGoPackages`, which
shells out to `go list` at eval time. To keep that call IFD-free, prefer a
source path (`./.`) or an eval-time fetcher (`builtins.fetchTarball`,
`builtins.fetchGit`). A derivation-backed value such as
`pkgs.fetchFromGitHub` is accepted, but the plugin must realise it at eval
time and emits an IFD warning.

| Attribute | Type | Modes | Description |
|-----------|------|-------|-------------|
| `src` | path | both | See above. |
| `pname` | string | both | Package name for the output derivation. |
| `version` | string | default only | Package version. The experimental builder ignores it (its wrapper produces a CA `.drv` whose name is derived from `pname` alone). |

## Optional attributes

| Attribute | Type | Default | Modes | Description |
|-----------|------|---------|-------|-------------|
| `goLock` | path or `null` | `null` (default) / required (experimental) | both | Path to `go2nix.toml`. In default mode, `null` enables [lockfile-free builds](lockfile-format.md#lockfile-free-builds). The experimental builder requires a lockfile. |
| `subPackages` | list of strings | `[ "." ]` | both | Packages to build, relative to `modRoot`. A `./` prefix is auto-added if missing. |
| `modRoot` | string | `"."` | both | Subdirectory within `src` containing `go.mod`. |
| `tags` | list of strings | `[]` | both | Go build tags. |
| `ldflags` | list of strings | `[]` | both | Flags passed to `go tool link` (`-s`, `-w`, `-X`, etc.). |
| `gcflags` | list of strings | `[]` | both | Extra flags passed to `go tool compile`. |
| `CGO_ENABLED` | `0`, `1`, or `null` | the scope's `goEnv.CGO_ENABLED` if set, else `null` (default mode); `null` (experimental) | both | Value of `CGO_ENABLED` for the eval-time `go list` and the build. With `null` the toolchain's default applies, and a package is built as cgo when `go list` reports `CgoFiles` for it, i.e. it has Go files that `import "C"`. |
| `pgoProfile` | path or `null` | `null` | both | Path to a pprof CPU profile for profile-guided optimization. The profile is passed to every `go tool compile` invocation, so changing it invalidates all package derivations. See [Go's PGO docs](https://go.dev/doc/pgo) for producing a profile. |
| `nativeBuildInputs` | list | `[]` | both | Extra build inputs for the final derivation. In experimental mode they land on the wrapper that runs `go2nix resolve`, not on the link derivation; give link-time libraries through `packageOverrides`. |
| `packageOverrides` | attrset | `{}` | both | Per-package customization (see below). |
| `doCheck` | bool | `true` | default only | Run tests. Matches `buildGoModule`'s default. See [Test Support](test-support.md). |
| `checkFlags` | list of strings | `[]` | default only | Flags passed verbatim to the compiled test binary, so in its own spelling: `-test.v`, `-test.run=^TestFoo$`, `-test.count=1` (not `go test`'s `-v`, `-run`). See [Test Support](test-support.md). |
| `extraMainSrcFiles` | list of strings | `[]` | default only | Extra `src`-relative paths (files or directories) kept in the filtered test source tree. Escape hatch for tests that read runtime files outside `testdata/` and `//go:embed`. See [Test Support](test-support.md#extramainsrcfiles). |
| `srcFilter` | function `path: type: bool` | keep everything | default only | Extra predicate, with `builtins.path`'s `filter` signature, ANDed into every filtered copy the builder makes of `src` (each local package's source and the test source tree). Pass the real source tree as `src` plus the membership a pre-filtered copy would have had (e.g. a `lib.fileset`'s files and their directories) instead of a `lib.fileset.toSource` / `builtins.path` copy: the builder walks and `go list`s `src` during evaluation, which over a store copy only works once that copy has been written, so a pre-copied `src` cannot be evaluated read-only against a store that has not seen it. Per-package store paths are unchanged as long as the predicate admits the same files. |
| `goProxy` | string or `null` | `null` | default only | `GOPROXY` for the module fetches and for the eval-time `go list`, written into the derivations. With `null` the fetches inherit `GOPROXY`/`NETRC` from the builder's environment (a daemon or a remote builder usually has none, and Go's default proxy applies). |
| `allowGoReference` | bool | `false` | default only | Allow the output to reference the Go toolchain. The output may never reference the filtered source tree (`mainSrc`); both are enforced with `disallowedReferences`. |
| `meta` | attrset | `{}` | default only | Nix meta attributes. |
| `contentAddressed` | bool | `false` | default only | Make the local-package derivations floating-CA with an extra `iface` (export-data) output, so private-symbol-only edits don't cascade, and the importcfg bundle floating-CA too. Third-party packages stay input-addressed. Requires the `ca-derivations` experimental feature; the final binary stays input-addressed. See [Incremental Builds → Early cutoff](incremental-builds.md#early-cutoff-with-contentaddressed--true) for details and limitations. |

Anything else you pass to `buildGoApplication` goes to `stdenv.mkDerivation`
unchanged (`preBuild`, `postInstall`, `outputs`, ...). `buildInputs`,
`disallowedReferences`, `env` and `passthru` are merged with the builder's
own. The result's `passthru` has `go`, `go2nix`, `goLock`, `packages`,
`localPackages`, `depsImportcfg`, `mainSrc`, `modulePath` and, with `doCheck`,
`testPackages` and `testDepsImportcfg`; see
[Troubleshooting](troubleshooting.md) for how to use them. `extraMainSrcFiles`
entries that do not exist under `src` are an error.

## What a call produces

One `buildGoApplication` call evaluates to one derivation, the application,
whose inputs are all the others:

| Derivation name | How many |
|-----------------|----------|
| `go-stdlib-<go version>[-<hash of goEnv>]` | one per scope, shared by every build in it (see [The Scope](scope.md#stdlib)) |
| `gomod-<module path>-<version>` | one per module the build uses |
| `gopkg-<import path>-<module version>` | one per third-party package the build reaches |
| `golocal-<import path>` | one per local package, and `golocal-<import path>-src` for its filtered source |
| `<pname>-deps-importcfg`, and `<pname>-test-deps-importcfg` with `doCheck` | one each |
| `<pname>-<version>` | the application: compiles the main packages, links, runs the tests |

Characters that cannot appear in a store path are replaced (`/` by `-`, `@`
by `_at_`, `~` by `_`). `result/bin/` holds one binary per entry of
`subPackages`: the package at the module root is named `pname`, any other
one after its directory (`./cmd/server` gives `bin/server`).

## `modRoot`

When building one module inside a larger source tree (e.g., a monorepo), set
`src` to the repository root and `modRoot` to the subdirectory containing
`go.mod`:

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./app/go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  modRoot = "app";
  subPackages = [ "cmd/server" ];
}
```

This is necessary when the module uses `replace` directives pointing to sibling
directories outside `modRoot`. The builder needs access to the full `src` tree,
with `modRoot` telling it where `go.mod` lives (a leading `./` is accepted). The filtered `mainSrc` for the
final derivation unions in those sibling replace directories, so `doCheck`
works regardless of `modRoot`.

## `subPackages`

List of packages to build, relative to `modRoot`. Each entry is a Go package
path like `"cmd/server"` or `"."` (the module root package).

A `./` prefix is added automatically if missing, so `"cmd/server"` and
`"./cmd/server"` are equivalent.

The default `[ "." ]` builds the package at `modRoot`.

## `packageOverrides`

Per-package customization keyed by Go import path or module path:

```nix
packageOverrides = {
  "github.com/mattn/go-sqlite3" = {
    nativeBuildInputs = [ pkg-config sqlite ];
  };
};
```

See [Package Overrides](package-overrides.md) for the lookup rules,
supported keys, cgo recipes, and mode differences.

## `mkGoEnv`

Both builders are accessed through a scope created by `mkGoEnv`:

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;

  # Optional:
  goEnv = { CGO_ENABLED = "0"; };
  netrcFile = ./my-netrc;
  nixPackage = pkgs.nixVersions.nix_2_34;  # required for experimental mode
};
```

| Attribute | Type | Default | Description |
|-----------|------|---------|-------------|
| `go` | derivation | required | Go toolchain. |
| `go2nix` | derivation | required | go2nix CLI binary. |
| `callPackage` | function | required | `pkgs.callPackage`. |
| `tags` | list of strings | `[]` | Stored on the scope, but neither builder reads it: pass `tags` to each `buildGoApplication` call. |
| `goEnv` | attrset | `{}` | Environment variables applied to stdlib compilation and, in default mode, to every compile and link in this scope (e.g. `GOEXPERIMENT`, `GOFIPS140`, `CGO_ENABLED`). In experimental mode they reach the stdlib and `go2nix resolve` only, not the per-package derivations. Of these only `CGO_ENABLED`, `GOOS` and `GOARCH` also reach the eval-time `go list`. Scope-level because the stdlib derivation is shared by every build in the scope. |
| `netrcFile` | path or `null` | `null` | `.netrc` file for private module authentication (see below). |
| `nixPackage` | derivation or `null` | `null` | Nix binary. Required for `buildGoApplicationExperimental`. |

## Cross-compilation

`GOOS` / `GOARCH` are read from `stdenv.hostPlatform.go`, so cross builds are
driven the standard nixpkgs way — pass a cross `pkgs` (e.g.
`pkgsCross.aarch64-multiplatform`) into `mkGoEnv` via `callPackage`, and the
resulting scope produces binaries for that target. `goEnv.GOOS` / `goEnv.GOARCH`
win over the platform's if you set them. Only `callPackage` has to come from
the cross package set: `go` runs on the build machine, so keep passing a
native one. The [Nix plugin](nix-plugin.md) is told the target `goos`/`goarch`
so build-tag evaluation matches the host platform. Default mode only.

## FIPS 140 mode (`GOFIPS140`)

Set `GOFIPS140` via the scope-level `goEnv` to build against the Go FIPS 140
crypto module, equivalent to `GOFIPS140=latest go build` (default mode; the
experimental builder does not pass `goEnv` to its compile and link steps):

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  goEnv = { GOFIPS140 = "latest"; };
};
```

The variable reaches both `go install std` (so the FIPS-aware stdlib is
compiled) and the link step, where go2nix emits the matching
`build GOFIPS140=` modinfo line and folds `fips140=on` into
`DefaultGODEBUG` — `go version -m` output is identical to a vanilla
`GOFIPS140=latest go build -trimpath`.

## Private modules (`netrcFile`)

Go modules hosted behind authentication (private Git repos, private proxies)
require credentials. Set `netrcFile` in `mkGoEnv` to a `.netrc` file:

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  netrcFile = ./secrets/netrc;
};
```

The file uses standard [`.netrc` format](https://www.gnu.org/software/inetutils/manual/html_node/The-_002enetrc-file.html):

```
machine github.com
login x-access-token
password ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

machine proxy.example.com
login myuser
password mytoken
```

The file is copied to `$HOME/.netrc` inside each module fetch derivation, which
covers a proxy that wants authentication (point `goProxy` at it). Go's
`direct` fallback shells out to `git` or another VCS tool, and the fetch
derivations have none on `PATH`, so do not count on it for private
repositories: serve them through a proxy.

In experimental mode, the file is passed as `--netrc-file` to
`go2nix resolve`, which forwards it to the module FODs built inside
the recursive-nix sandbox.

**Note:** Any value passed to `netrcFile` reaches a fixed-output derivation
and is therefore world-readable in `/nix/store`. There is currently no
mechanism to keep the credential out of the store entirely; use a
low-privilege, repository-scoped token (and rotate it) rather than a
personal credential.
