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
| `version` | string | default only | Package version. The experimental builder does not accept this attribute (its wrapper produces a CA `.drv` whose name is derived from `pname` alone). |

## Optional attributes

| Attribute | Type | Default | Modes | Description |
|-----------|------|---------|-------|-------------|
| `goLock` | path or `null` | `null` (default) / required (experimental) | both | Path to `go2nix.toml`. In default mode, `null` enables [lockfile-free builds](lockfile-format.md#lockfile-free-builds). The experimental builder requires a lockfile. |
| `subPackages` | list of strings | `[ "." ]` | both | Packages to build, relative to `modRoot`. A `./` prefix is auto-added if missing. |
| `modRoot` | string | `"."` | both | Subdirectory within `src` containing `go.mod`. |
| `tags` | list of strings | `[]` | both | Go build tags. |
| `ldflags` | list of strings | `[]` | both | Flags passed to `go tool link` (`-s`, `-w`, `-X`, etc.). |
| `gcflags` | list of strings | `[]` | both | Extra flags passed to `go tool compile`. |
| `CGO_ENABLED` | `0`, `1`, or `null` | `null` (auto) | both | Override CGO detection. When `null`, CGO is enabled per-package based on the presence of C/C++ files. |
| `pgoProfile` | path or `null` | `null` | both | Path to a pprof CPU profile for profile-guided optimization. The profile is passed to every `go tool compile` invocation, so changing it invalidates all package derivations. See [Go's PGO docs](https://go.dev/doc/pgo) for producing a profile. |
| `nativeBuildInputs` | list | `[]` | both | Extra build inputs for the final derivation. |
| `packageOverrides` | attrset | `{}` | both | Per-package customization (see below). |
| `doCheck` | bool | `true` | default only | Run tests. Matches `buildGoModule`'s default. See [Test Support](test-support.md). |
| `checkFlags` | list of strings | `[]` | default only | Flags passed to the compiled test binary (e.g., `-v`, `-count=1`). See [Test Support](test-support.md). |
| `extraMainSrcFiles` | list of strings | `[]` | default only | Extra `src`-relative paths (files or directories) kept in the filtered test source tree. Escape hatch for tests that read runtime files outside `testdata/` and `//go:embed`. See [Test Support](test-support.md#extramainsrcfiles). |
| `goProxy` | string or `null` | `null` | default only | Custom GOPROXY URL. |
| `allowGoReference` | bool | `false` | default only | Allow the output to reference the Go toolchain. |
| `meta` | attrset | `{}` | default only | Nix meta attributes. |
| `contentAddressed` | bool | `false` | default only | Make per-package and importcfg derivations floating-CA and add an `iface` (export-data) output so private-symbol-only edits don't cascade. Requires the `ca-derivations` experimental feature; the final binary stays input-addressed. See [Incremental Builds → Early cutoff](incremental-builds.md#early-cutoff-with-contentaddressed--true) for details and limitations. |

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
with `modRoot` telling it where `go.mod` lives. The filtered `mainSrc` for the
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
  tags = [ "nethttpomithttp2" ];
  netrcFile = ./my-netrc;
  nixPackage = pkgs.nixVersions.nix_2_34;  # required for experimental mode
};
```

| Attribute | Type | Default | Description |
|-----------|------|---------|-------------|
| `go` | derivation | required | Go toolchain. |
| `go2nix` | derivation | required | go2nix CLI binary. |
| `callPackage` | function | required | `pkgs.callPackage`. |
| `tags` | list of strings | `[]` | Build tags applied to all builds in this scope. |
| `goEnv` | attrset | `{}` | Environment variables applied to stdlib compilation and every `go tool` invocation in this scope (e.g. `GOEXPERIMENT`, `GOFIPS140`). Scope-level because the stdlib derivation is shared by every build in the scope. |
| `netrcFile` | path or `null` | `null` | `.netrc` file for private module authentication (see below). |
| `nixPackage` | derivation or `null` | `null` | Nix binary. Required for `buildGoApplicationExperimental`. |

## Cross-compilation

`GOOS` / `GOARCH` are read from `stdenv.hostPlatform.go`, so cross builds are
driven the standard nixpkgs way — pass a cross `pkgs` (e.g.
`pkgsCross.aarch64-multiplatform`) into `mkGoEnv` via `callPackage`, and the
resulting scope produces binaries for that target. The
[Nix plugin](nix-plugin.md) is told the target `goos`/`goarch` so build-tag
evaluation matches the host platform.

## FIPS 140 mode (`GOFIPS140`)

Set `GOFIPS140` via the scope-level `goEnv` to build against the Go FIPS 140
crypto module, equivalent to `GOFIPS140=latest go build`:

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

The file is copied to `$HOME/.netrc` inside each module fetch derivation.
Go's default `GOPROXY` (`https://proxy.golang.org,direct`) falls back to
direct VCS access when the proxy returns 404, so `netrcFile` covers both
proxy-authenticated and direct-access private module setups.

In experimental mode, the file is passed as `--netrc-file` to
`go2nix resolve`, which forwards it to the module FODs built inside
the recursive-nix sandbox.

**Note:** Any value passed to `netrcFile` reaches a fixed-output derivation
and is therefore world-readable in `/nix/store`. There is currently no
mechanism to keep the credential out of the store entirely; use a
low-privilege, repository-scoped token (and rotate it) rather than a
personal credential.
