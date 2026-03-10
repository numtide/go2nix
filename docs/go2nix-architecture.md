# go2nix Architecture

Technical reference for the go2nix build system.

## Overview

go2nix builds Go applications in Nix with three modes that share the same
Go CLI and lockfile infrastructure but differ in how they create derivations.

The system has two components:

1. **A Go CLI** (`go2nix`) that generates lockfiles, discovers packages and
   files, compiles packages, and validates lockfile consistency.
1. **A Nix library** that reads lockfiles and builds Go applications using
   one of three modes.

## Builder modes

| Mode | How it works | Lockfile | Caching | Nix features |
|------|-------------|----------|---------|--------------|
| **Vendor** | `go build` with vendored deps | `[mod]` only | Per-module | None |
| **DAG** | `go tool compile/link` per-package | `[mod]` + `[pkg]` | Per-package | None |
| **Dynamic** | Recursive-nix, DAG at build time | `[mod]` only | Per-package | `dynamic-derivations`, `ca-derivations`, `recursive-nix` |

### Vendor mode

Downloads modules, creates a vendor tree via symlinks, runs `go build`.
Derived from [gomod2nix](https://github.com/nix-community/gomod2nix).
Works everywhere. A change to any dependency rebuilds the entire application.

See [vendor-mode.md](vendor-mode.md) for internals.

### DAG mode

Each third-party Go package becomes its own Nix derivation. go2nix calls
`go tool compile` and `go tool link` directly — bypassing `go build`. This
gives Nix full control over the dependency graph at package granularity.
When a dependency changes, only affected packages rebuild.

See [dag-mode.md](dag-mode.md) for internals.

### Dynamic mode

Same per-package granularity as DAG mode, but the package graph is discovered
at build time using recursive-nix and content-addressed (CA) derivations.
Only needs `[mod]` in the lockfile — faster lockfile generation, smaller
diffs.

See [dynamic-derivations.md](dynamic-derivations.md) for internals.

### Choosing a mode

`buildGoApplication` auto-selects: dynamic mode when `builtins.outputOf`
is available and `nixPackage` is set, otherwise DAG mode. Use the explicit
builders to override:

```nix
goEnv.buildGoApplicationVendorMode { ... }
goEnv.buildGoApplicationDAGMode { ... }
goEnv.buildGoApplicationDynamicMode { ... }
```

## Nix directory layout

```
nix/
├── mk-go-env.nix          # Entry point: creates Go toolchain scope
├── scope.nix              # Self-referential package set (lib.makeScope)
├── stdlib.nix             # Shared: compiled Go standard library
├── helpers.nix            # Shared: sanitizeName, escapeModPath, etc.
├── dag/                   # DAG mode
│   ├── default.nix        #   buildGoApplicationDAGMode
│   ├── fetch-go-module.nix#   FOD fetcher (GOMODCACHE layout)
│   ├── process-lockfile.nix#  TOML → {modules, packages}
│   ├── go2nix.wasm        #   Fast TOML parser (optional)
│   └── hooks/             #   Setup hooks (compile, link, env)
├── vendor/                # Vendor mode
│   ├── default.nix        #   buildGoApplicationVendorMode
│   ├── parser.nix         #   Pure-Nix go.mod parser
│   ├── fetch.sh           #   Module fetch script
│   ├── symlink/           #   Vendor symlink utility
│   ├── install/           #   Dev dependency installer
│   └── mvscheck/          #   MVS tidiness checker
└── dynamic/               # Dynamic mode
    └── default.nix        #   buildGoApplicationDynamicMode
```

### Entry point: mk-go-env.nix

```nix
goEnv = import ./nix/mk-go-env.nix {
  inherit go go2nix;
  inherit (pkgs) callPackage;
  tags = [ "nethttpomithttp2" ];  # optional
  nixPackage = pkgs.nix;          # optional, enables dynamic mode
};
```

Creates a scope via `scope.nix` containing all three builders plus shared
toolchain.

### Package scope: scope.nix

Uses `lib.makeScope newScope` to create a self-referential package set.
Everything within the scope shares the same Go version, build tags, and
go2nix binary.

Exposes:
- `buildGoApplication` — auto-selects dynamic or DAG
- `buildGoApplicationDAGMode`
- `buildGoApplicationVendorMode`
- `buildGoApplicationDynamicMode`
- `go`, `go2nix`, `stdlib`, `hooks`, `fetchers`, `helpers`

### Shared: stdlib.nix

Compiles the entire Go standard library:

```
GODEBUG=installgoroot=all GOROOT=. go install -v --trimpath std
```

Output: `$out/<pkg>.a` for each stdlib package + `$out/importcfg`. Shared by
DAG and dynamic modes.

### Shared: helpers.nix

Pure Nix utility functions:
- `sanitizeName` — `/` → `-`, `+` → `_` for derivation names.
- `removePrefix` — Substring after a known prefix.
- `escapeModPath` — Go module case-escaping (`A` → `!a`).

## Staleness detection

| When | What | How |
|------|------|-----|
| Generation | MVS consistency | `go list -json -deps` resolves actual versions |
| Nix eval | Missing packages | Package not in lockfile → error |
| Build time | Lockfile consistency | `go2nix check --lockfile` or `mvscheck` |
| Build time | Tidiness | `go mod graph` against GOMODCACHE from vendor tree |

## Further reading

- [Lockfile format](lockfile-format.md)
- [CLI reference](cli-reference.md)
- [Compilation pipeline](compilation-pipeline.md)
- [DAG mode](dag-mode.md)
- [Vendor mode](vendor-mode.md)
- [Dynamic mode](dynamic-derivations.md)
