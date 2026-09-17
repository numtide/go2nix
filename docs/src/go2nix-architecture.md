# go2nix Architecture

Technical reference for the go2nix build system.

## Overview

go2nix builds Go applications in Nix with two modes that share the same
Go CLI and lockfile infrastructure but differ in how they create derivations.

The system has three components:

1. **A Go CLI** (`go2nix`) that generates and validates lockfiles and is what
   the derivations run: it compiles one package, links a binary, and builds
   and runs the tests.
1. **A Nix library** (`nix/`) that turns the package graph into derivations,
   in one of two modes.
1. **A Nix plugin** (`packages/go2nix-nix-plugin/`, a Rust core and a C++
   shim) that gives the default mode its package graph at eval time. It is
   built separately and has to be loaded into the evaluator.

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

See [Builder Modes](modes/) for the full comparison, requirements,
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
│   ├── fetch-go-module.nix #  FOD fetcher (one module's extracted source tree)
│   └── hooks/             #   Setup hooks (compile, link, env)
└── dynamic/               # Experimental mode (recursive-nix)
    └── default.nix        #   buildGoApplicationExperimental
```

### Entry point: mk-go-env.nix

```nix
goEnv = go2nix.lib.mkGoEnv {        # == import ./nix/mk-go-env.nix inside this repo
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;   # the CLI, not the flake
  goEnv = { CGO_ENABLED = "0"; };              # optional, env for stdlib and every go tool call
  netrcFile = null;                            # optional, for private modules
  nixPackage = pkgs.nixVersions.nix_2_34;      # optional, enables experimental mode
};
```

Creates a scope via `scope.nix` containing both builders plus shared
toolchain.

### Package scope: scope.nix

Uses `lib.makeScope newScope` to create a self-referential package set.
Everything within the scope shares the same Go toolchain, `goEnv`, standard
library and go2nix binary. (`mkGoEnv` also accepts `tags` and stores it on the
scope, but neither builder reads it: build tags are the per-call `tags`
argument.)

Exposes:

- `buildGoApplication` — default mode (eval-time per-package DAG)
- `buildGoApplicationExperimental` — experimental mode (recursive-nix)
- `go`, `go2nix`, `stdlib`, `hooks`, `fetchers` (`fetchGoModule`), `helpers`,
  and `goEnv` (the env attrset, with `GOOS`/`GOARCH` defaulted in when
  cross-compiling)

### Shared: stdlib.nix

Compiles the entire Go standard library:

```
GODEBUG=installgoroot=all GOROOT="$NIX_BUILD_TOP" go install -v --trimpath std
```

after copying the toolchain's `src`, `pkg` and `lib` there. Output:
`$out/<pkg>.a` for each stdlib package + `$out/importcfg`. There is one such
derivation per toolchain and scope `goEnv` (the variables are exported before
the build and a hash of them is part of the name), shared by every build in
the scope and by both modes.

### Shared: helpers.nix

Pure Nix utility functions:

- `sanitizeName` — `/` → `-`, `~` → `_`, `@` → `_at_` for derivation names, and anything longer than 160 characters is cut and given an 8-hex-digit hash suffix. The Go and Rust counterparts (`pkg/nixdrv/sanitize.go`, `resolve.rs`) additionally replace characters outside `[a-zA-Z0-9+-._?=]`; the three agree on valid import paths and must be kept in sync.
- `removePrefix` — Substring after a known prefix.
- `escapeModPath` — Go module case-escaping (`A` → `!a`).
- `normalizeSubPackages` — adds the missing `./` to `subPackages` entries.
- `goModLocalReplaceDirs`, `parseLocalReplaces` — read the filesystem `replace` targets out of a `go.mod`, for callers that build their own source filter.

## Staleness detection

A lockfile is checked when it is generated and again at build time, by
`link-binary`; a module the graph needs and the lockfile lacks already fails
evaluation — see
[Lockfile Format → Staleness detection](lockfile-format.md#staleness-detection)
for the full table. The `go2nix check` subcommand can also be used standalone
to verify a lockfile without building.

## Further reading

- [Builder Modes](modes/)
- [Nix Plugin](nix-plugin.md)
- [Incremental Builds](incremental-builds.md)
- [Builder API](builder-api.md)
- [Lockfile format](lockfile-format.md)
- [CLI reference](cli-reference.md)
