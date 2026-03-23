# go2nix Architecture

Technical reference for the go2nix build system.

## Overview

go2nix builds Go applications in Nix with two modes that share the same
Go CLI and lockfile infrastructure but differ in how they create derivations.

The system has two components:

1. **A Go CLI** (`go2nix`) that generates lockfiles, discovers packages and
   files, compiles packages, and validates lockfile consistency.
1. **A Nix library** that reads lockfiles and builds Go applications using
   one of two modes.

## Design context

go2nix builds Go applications at package granularity rather than treating
`go build` as a single opaque step. The approach is architecturally inspired
by Bazel's `rules_go` — both systems work from an explicit package graph —
but go2nix has a much narrower scope: bring package-graph-aware Go builds to
Nix derivations and lockfiles, not replicate a full Bazel rule ecosystem.

### Comparison with other Nix Go builders

| Tool | Granularity | Key difference from go2nix |
|------|-------------|---------------------------|
| `buildGoModule` | App-level (one fetch + one build derivation) | Nix doesn't model the Go package graph; any change rebuilds the whole app |
| `gomod2nix` | Module-level (lockfile-driven offline builds) | Focuses on locking and fetching modules, not per-package compilation |
| `gobuild.nix` | Module-level (`GOCACHEPROG`-backed cache reuse) | Per-module derivations, not per-package; different caching layer |
| `nix-gocacheprog` | Impure shared cache | Optimization for local iteration speed, not a pure builder |
| **go2nix** | Package-level (per-package derivations) | Discovers the import graph and compiles each package as its own derivation |

## Builder modes

| Mode | How it works | Lockfile | Caching | Nix features |
|------|-------------|----------|---------|--------------|
| **Default** | `go tool compile/link` per-package | `[mod]` + optional `[replace]` | Per-package | go2nix-nix-plugin |
| **Experimental** | Recursive-nix, per-package at build time | `[mod]` + optional `[replace]` | Per-package | `dynamic-derivations`, `ca-derivations`, `recursive-nix` |

### Default mode

Go packages are compiled as Nix derivations at eval time: third-party
packages, local packages, and optionally test-only third-party packages when
checks are enabled. go2nix calls `go tool compile` and `go tool link`
directly, bypassing `go build`. This gives Nix full control over the
dependency graph at package granularity. The package graph is discovered at
eval time by the go2nix-nix-plugin (`builtins.resolveGoPackages`), which runs
`go list` against the source tree. When a dependency changes, only affected
packages rebuild.

See [default-mode.md](modes/default-mode.md) for details.

### Experimental mode

Same per-package granularity as the default mode, but the package graph is
discovered at build time using recursive-nix and content-addressed (CA)
derivations. The lockfile stays package-graph-free because dependency
discovery is deferred to the build.

See [experimental-mode.md](modes/experimental-mode.md) for details.

### Choosing a mode

`buildGoApplication` uses the default mode. Use `buildGoApplicationExperimental`
only if you have Nix >= 2.34 with the required experimental features enabled:

```nix
# Default (recommended):
goEnv.buildGoApplication { ... }

# Experimental (requires nix experimental features):
goEnv.buildGoApplicationExperimental { ... }
```

## Nix directory layout

```
nix/
├── mk-go-env.nix          # Entry point: creates Go toolchain scope
├── scope.nix              # Self-referential package set (lib.makeScope)
├── stdlib.nix             # Shared: compiled Go standard library
├── helpers.nix            # Shared: sanitizeName, escapeModPath, etc.
├── dag/                   # Default mode (eval-time DAG)
│   ├── default.nix        #   buildGoApplication
│   ├── fetch-go-module.nix#   FOD fetcher (GOMODCACHE layout)
│   └── hooks/             #   Setup hooks (compile, link, env)
└── dynamic/               # Experimental mode (recursive-nix)
    └── default.nix        #   buildGoApplicationExperimental
```

### Entry point: mk-go-env.nix

```nix
goEnv = import ./nix/mk-go-env.nix {
  inherit go go2nix;
  inherit (pkgs) callPackage;
  tags = [ "nethttpomithttp2" ];  # optional
  nixPackage = pkgs.nix_234;      # optional, enables experimental mode
};
```

Creates a scope via `scope.nix` containing both builders plus shared
toolchain.

### Package scope: scope.nix

Uses `lib.makeScope newScope` to create a self-referential package set.
Everything within the scope shares the same Go version, build tags, and
go2nix binary.

Exposes:

- `buildGoApplication` — default mode (eval-time per-package DAG)
- `buildGoApplicationExperimental` — experimental mode (recursive-nix)
- `go`, `go2nix`, `stdlib`, `hooks`, `fetchers`, `helpers`

### Shared: stdlib.nix

Compiles the entire Go standard library:

```
GODEBUG=installgoroot=all GOROOT=. go install -v --trimpath std
```

Output: `$out/<pkg>.a` for each stdlib package + `$out/importcfg`. Shared by
both modes.

### Shared: helpers.nix

Pure Nix utility functions:

- `sanitizeName` — Whitelist `[a-zA-Z0-9+-._?=]`, `/` → `-`, `~` → `_`, `@` → `_at_` for derivation names.
- `removePrefix` — Substring after a known prefix.
- `escapeModPath` — Go module case-escaping (`A` → `!a`).

## Staleness detection

| When | What | Applies to | How |
|------|------|-----------|-----|
| Generation | MVS consistency | All modes | `go list -json -deps` resolves actual versions |
| Nix eval | Package graph | Default only | `builtins.resolveGoPackages` runs `go list` at eval time |
| Build time | Lockfile consistency | Default only | `link-binary` validates lockfile against `go.mod` via `mvscheck.CheckLockfile` |

The `go2nix check` subcommand can also be used standalone to verify a
lockfile without building.

## Further reading

- [Lockfile format](lockfile-format.md)
- [CLI reference](cli-reference.md)
- [Default mode](modes/default-mode.md)
- [Experimental mode](modes/experimental-mode.md)
