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

| Attribute | Type | Modes | Description |
|-----------|------|-------|-------------|
| `src` | path | both | Source tree. For monorepos with `modRoot`, this should be the repository root. |
| `goLock` | path | both | Path to `go2nix.toml` lockfile. |
| `pname` | string | both | Package name for the output derivation. |
| `version` | string | default only | Package version. The experimental builder does not accept this attribute. |

## Optional attributes

| Attribute | Type | Default | Modes | Description |
|-----------|------|---------|-------|-------------|
| `subPackages` | list of strings | `[ "." ]` | both | Packages to build, relative to `modRoot`. A `./` prefix is auto-added if missing. |
| `modRoot` | string | `"."` | both | Subdirectory within `src` containing `go.mod`. |
| `tags` | list of strings | `[]` | both | Go build tags. |
| `ldflags` | list of strings | `[]` | both | Flags passed to `go tool link` (`-s`, `-w`, `-X`, etc.). |
| `gcflags` | list of strings | `[]` | both | Extra flags passed to `go tool compile`. |
| `CGO_ENABLED` | `0`, `1`, or `null` | `null` (auto) | both | Override CGO detection. When `null`, CGO is enabled per-package based on the presence of C/C++ files. |
| `pgoProfile` | path or `null` | `null` | both | Path to a pprof CPU profile for profile-guided optimization. |
| `nativeBuildInputs` | list | `[]` | both | Extra build inputs for the final derivation. |
| `packageOverrides` | attrset | `{}` | both | Per-package customization (see below). |
| `doCheck` | bool | `modRoot == "."` | default only | Run tests. Defaults to `false` when `modRoot` is set, because test discovery may not find local replace targets outside the module root. |
| `checkFlags` | list of strings | `[]` | default only | Flags passed to the compiled test binary (e.g., `-v`, `-count=1`). |
| `goProxy` | string or `null` | `null` | default only | Custom GOPROXY URL. |
| `allowGoReference` | bool | `false` | default only | Allow the output to reference the Go toolchain. |
| `meta` | attrset | `{}` | default only | Nix meta attributes. |
| `contentAddressed` | bool | `false` | default only | Make per-package and importcfg derivations [floating-CA](https://nix.dev/manual/nix/development/development/experimental-features.html#xp-feature-ca-derivations) so byte-identical rebuilds short-circuit downstream recompiles (early cutoff). Each per-package derivation also gains an `iface` output containing only the export data (`.x` file via `go tool compile -linkobj`); downstream compiles depend on `iface`, so private-symbol changes that don't alter export data don't cascade (mirrors rules_go's `.x` model). Requires the `ca-derivations` experimental feature; the final binary stays input-addressed. Limitation on the iface cutoff: adding the *first* package-level initializer to a previously-init-free package still flips a bit in the `.x` (this is rare in practice). |

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
with `modRoot` telling it where `go.mod` lives.

When `modRoot != "."`, `doCheck` defaults to `false` because the filtered
source tree for tests may not include out-of-tree replace targets. Override
with `doCheck = true` if your layout doesn't use out-of-tree replaces.

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

Override lookup: exact import path first, then module path.

### Supported override attributes

| Attribute | Default mode | Experimental mode |
|-----------|-------------|-------------------|
| `nativeBuildInputs` | yes | yes |
| `env` | yes | no |

The `env` attribute sets environment variables on the per-package derivation:

```nix
packageOverrides = {
  "github.com/example/pkg" = {
    env = {
      CGO_CFLAGS = "-I${libfoo.dev}/include";
    };
  };
};
```

The experimental builder rejects unknown attributes (including `env`) at eval
time. Derivations are synthesized at build time by `go2nix resolve`, so only
`nativeBuildInputs` (store paths) can be forwarded.

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
| `netrcFile` | path or `null` | `null` | `.netrc` file for private module authentication (see below). |
| `nixPackage` | derivation or `null` | `null` | Nix binary. Required for `buildGoApplicationExperimental`. |

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

**Note:** The netrc file becomes a Nix store path, so its contents are
world-readable in `/nix/store`. For sensitive credentials, consider using
a secrets manager or a file reference outside the store (e.g., via
`builtins.readFile` from a non-tracked path).
