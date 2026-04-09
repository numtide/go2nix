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

For how go2nix compares to `buildGoModule`, `gomod2nix`, `gobuild.nix`, and
`nix-gocacheprog`, see the
[comparison table in the README](https://github.com/numtide/go2nix#comparison-with-nix-alternatives).

## Builder modes

go2nix ships two builders that share the same lockfile and CLI but differ in
*when* the package graph is discovered:

- **Default mode** (`buildGoApplication`) turns each Go package into its own
  Nix derivation. go2nix calls `go tool compile` and `go tool link` directly
  instead of `go build`, giving Nix full control of the dependency graph at
  package granularity. The [go2nix-nix-plugin](nix-plugin.md)
  (`builtins.resolveGoPackages`) discovers the package graph at eval time by
  running `go list` against the source tree, so when a dependency changes
  only the affected packages rebuild.

- **Experimental mode** (`buildGoApplicationExperimental`) provides the same
  per-package granularity, but discovers the package graph at build time
  using recursive-nix and content-addressed derivations. Dependency
  discovery is deferred to the build, so no plugin is required.

See [Builder Modes](modes/README.md) for the full comparison, requirements,
and how to choose between them.

## Nix directory layout

```
nix/
├── mk-go-env.nix          # Entry point: creates Go toolchain scope
├── scope.nix              # Self-referential package set (lib.makeScope)
├── stdlib.nix             # Shared: compiled Go standard library
├── helpers.nix            # Shared: sanitizeName, escapeModPath, etc.
├── dag/                   # Default mode (eval-time DAG)
│   ├── default.nix        #   buildGoApplication
│   ├── fetch-go-module.nix #  FOD fetcher (GOMODCACHE layout)
│   └── hooks/             #   Setup hooks (compile, link, env)
└── dynamic/               # Experimental mode (recursive-nix)
    └── default.nix        #   buildGoApplicationExperimental
```

### Entry point: mk-go-env.nix

```nix
goEnv = go2nix.lib.mkGoEnv {        # == import ./nix/mk-go-env.nix inside this repo
  inherit go go2nix;
  inherit (pkgs) callPackage;
  tags = [ "nethttpomithttp2" ];    # optional
  nixPackage = pkgs.nix_234;        # optional, enables experimental mode
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

The lockfile is validated at generation, eval, and build time — see
[Lockfile Format → Staleness detection](lockfile-format.md#staleness-detection)
for the full table. The `go2nix check` subcommand can also be used standalone
to verify a lockfile without building.

## Further reading

- [Builder Modes](modes/README.md)
- [Nix Plugin](nix-plugin.md)
- [Incremental Builds](incremental-builds.md)
- [Builder API](builder-api.md)
- [Lockfile format](lockfile-format.md)
- [CLI reference](cli-reference.md)
