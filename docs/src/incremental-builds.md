# Incremental Builds

This page explains what go2nix actually puts in the Nix store, what gets
reused on rebuilds, and how that differs from `buildGoModule`.

If you only want the API surface, see [Builder API](builder-api.md). If you
want the step-by-step eval flow, see [Default Mode](modes/default-mode.md).

## The shape of a build

### `buildGoModule` (nixpkgs)

```
┌──────────────────────────┐     ┌──────────────────────────┐
│ vendor FOD               │ ──▶ │ app derivation           │
│ (all modules, one hash)  │     │ (go build ./..., 1 drv)  │
└──────────────────────────┘     └──────────────────────────┘
```

Two derivations total. Any change to any `.go` file rebuilds the whole app
derivation; any `go.sum` change re-downloads the entire vendor tree.

### go2nix (default mode)

```
  module FODs                        one fixed-output derivation (FOD)
       │                             per module@version the build uses
       ▼
  third-party package drvs (.a)      one per imported third-party package
       │                  │
       ▼                  ▼
  local package drvs     importcfg bundle     bundle = stdlib + third-party
  (.a; plus .x with      (one per app)        entries, no local packages
   contentAddressed)          │
       │                      │
       └──────────┬───────────┘
                  ▼
              app drv                compiles the main package(s), links,
                                     and runs the tests (doCheck)

  stdlib drv ──► an input of every compile and of the bundle
                 (Go toolchain + scope goEnv only)
```

`.a` = compiled package archive; `.x` = export-data interface, which only
local packages get and only with `contentAddressed = true` (see
[Early cutoff](#early-cutoff-with-contentaddressed--true) below). The
*importcfg* is a file that maps each import path to its compiled `.a`
archive in the store — `go tool compile` and `go tool link` read it instead
of searching `GOPATH`.

For a non-trivial application this is hundreds to thousands of derivations
instead of two — but almost all of them are reusable across rebuilds.

## What gets cached

| Layer | One derivation per | Cache key (informally) | Rebuilds when |
|-------|--------------------|------------------------|---------------|
| stdlib | Go toolchain and scope `goEnv` | Go version + `goEnv` (`GOOS`/`GOARCH` when cross-compiling, `CGO_ENABLED`, `GOFIPS140`, ...) | Go is bumped or `goEnv` changes |
| module FOD | module `path@version` the build uses | module path@version + NAR hash | that module is bumped |
| third-party package | imported package | module FOD + import deps + tags + gcflags | the module or any of its transitive deps change |
| local package | local Go package | the package's directory (every file in it, `_test.go` and `testdata/` included, nested packages excluded) + import deps | any file in that directory or a dep changes |
| importcfg bundle | app | the stdlib and the third-party package outputs | a third-party package output changes |
| app | app | importcfg bundle + local package archives + the filtered source (`mainSrc`: with `doCheck`, every local package's sources, tests and `testdata/`) | anything above changes |

Third-party module FODs and third-party package derivations are shared
between every application in the flake (and across flakes, via the binary
cache). Bumping a single module re-fetches one FOD and recompiles only the
packages that transitively import it.

Local package derivations use a `builtins.path`-filtered source: only the
package's own directory is hashed — its whole subtree, minus nested package
directories and nested modules, and whatever `srcFilter` rejects — so editing
`pkg/a/a.go` does not change the input hash of the `pkg/b` derivation unless
`b` imports `a`. No `go.mod` comes along (the `go` directive is passed in
separately). Everything in the directory counts, not just what gets compiled:
embedded assets, but also `_test.go` files, `testdata/` and a README next to
the sources participate in the package's cache key.

## Rebuild propagation

When you edit a single local package, only the **reverse-dependency cone**
of that package rebuilds:

1. The edited package recompiles.
1. Each package that imports it (directly or transitively) recompiles.
1. The final derivation rebuilds: it compiles the main package, links, and
   runs the tests. The importcfg bundle does not: it only holds standard
   library and third-party entries, which a local edit does not touch.

Packages outside the cone keep their existing store paths and are not
rebuilt.

The same reasoning for other kinds of change:

| You change | What rebuilds |
|------------|---------------|
| a file in a local package's directory (source, test or `testdata/`) | that package, the local packages that import it directly or transitively, and the app. With `contentAddressed = true` the dependents are skipped when the package's export data came out the same, which is always the case for a test-only edit |
| an import between packages that already exist | the importing package and its cone. Nothing to regenerate: the lockfile pins modules, not the graph |
| one module's version | its FOD, the packages of that module, everything that imports them, the importcfg bundle and the app |
| a test-only dependency | its `testPackages` derivations, the test importcfg bundle and the app (relink, tests re-run) |
| `ldflags`, `checkFlags` | the app only |
| `tags`, `gcflags`, `pgoProfile` | every package compile (they are part of each compile manifest), then the bundle and the app. The stdlib is not affected |
| the scope's `goEnv`, or the Go toolchain | the stdlib, and everything after it |

To get a feel for how big the cone is in your project, see
[Benchmarking](benchmarking.md).

## Early cutoff with `contentAddressed = true`

By default, per-package derivations are input-addressed: if a package's
*inputs* change, every downstream derivation gets a new store path even if
the compiled output happens to be byte-identical.

Setting `contentAddressed = true` opts into two coupled mechanisms:

- **Floating-CA outputs.** Local-package derivations and the importcfg bundle
  become content-addressed, so a rebuild that produces a byte-identical `.a`
  resolves to the same store path and short-circuits downstream rebuilds.
  Third-party packages stay input-addressed: their source never changes, so
  CA would only add resolution overhead.
- **`iface` output split.** Each local-package derivation gains a second
  `iface` output containing only the export data (the `.x` file produced by
  `go tool compile -linkobj`). Downstream compiles depend on `iface`
  instead of the full `.a`, so changes to private symbols that don't alter
  the package's exported API don't cascade. This mirrors the `.x` model
  used by Bazel's `rules_go`.

The two are coupled by design: CA without `iface` only short-circuits
comment-only edits, and `iface` without CA can't cut off anything because
the input-addressed `.x` path still changes whenever `src` changes.

> **Requires** the `ca-derivations` experimental feature in Nix. The final
> binary stays input-addressed.
>
> **Known limitation:** adding the *first* package-level initializer to a
> previously init-free package still flips a bit in the `.x` file, so that
> particular edit cascades even though the API didn't change. This is rare
> in practice.

## The cost: eval time

go2nix trades build time for eval time. Every `nix build` evaluation:

1. Calls `builtins.resolveGoPackages` (the [Nix plugin](nix-plugin.md)),
   which runs `go list -json -deps` against your source tree — and a second
   `go list -deps -test` pass when `doCheck` is on, which is the default.
1. Instantiates one derivation per package in the resulting graph.

For a large application (~3,500 packages) the warm-cache `go list` step
takes on the order of **a few hundred milliseconds**, and instantiation
adds a similar amount on top. This is the floor on every rebuild — even a
no-change rebuild — and is the main reason go2nix is overkill for small
single-binary projects.

The plugin call is impure (it reads `GOMODCACHE`), so the result is not
cached by the Nix evaluator across invocations. See
[Nix Plugin](nix-plugin.md) for details.
