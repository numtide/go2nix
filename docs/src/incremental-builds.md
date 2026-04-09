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
┌────────────┐  ┌────────────┐         ┌────────────┐
│ module FOD │  │ module FOD │   ...   │ module FOD │   one per go.sum entry
└─────┬──────┘  └─────┬──────┘         └─────┬──────┘
      │               │                      │
┌─────▼──────┐  ┌─────▼──────┐         ┌─────▼──────┐
│ pkg drv    │  │ pkg drv    │   ...   │ pkg drv    │   one per Go package
│ (.a/.x)    │  │ (.a/.x)    │         │ (.a/.x)    │   (third-party + local)
└─────┬──────┘  └─────┬──────┘         └─────┬──────┘
      └───────┬───────┴──────────┬───────────┘
        ┌─────▼─────┐      ┌─────▼─────┐
        │ importcfg │      │ stdlib    │              bundle drvs
        │ bundle    │      │ drv       │
        └─────┬─────┘      └─────┬─────┘
              └────────┬─────────┘
                 ┌─────▼─────┐
                 │ app drv   │                        link only
                 │ (link)    │
                 └───────────┘
```

For a non-trivial application this is hundreds to thousands of derivations
instead of two — but almost all of them are reusable across rebuilds.

## What gets cached

| Layer | One derivation per | Cache key (informally) | Rebuilds when |
|-------|--------------------|------------------------|---------------|
| stdlib | Go toolchain | Go version + GOOS/GOARCH + tags | Go is bumped |
| module FOD | `go.sum` line | module path@version + NAR hash | that module is bumped in `go.sum` |
| third-party package | imported package | module FOD + import deps + gcflags | the module or any of its transitive deps change |
| local package | local Go package | filtered package directory + import deps | a `.go` file in that directory or a dep changes |
| importcfg bundle | app | the set of compiled package outputs | any package output changes |
| app | app | importcfg bundle + main package source | anything above changes |

Third-party module FODs and third-party package derivations are shared
between every application in the flake (and across flakes, via the binary
cache). Bumping a single module re-fetches one FOD and recompiles only the
packages that transitively import it.

Local package derivations use a `builtins.path`-filtered source: only the
package's own directory (plus its parent `go.mod`/`go.sum`) is hashed, so
editing `pkg/a/a.go` does not change the input hash of the `pkg/b`
derivation unless `b` imports `a`.

## Rebuild propagation

When you edit a single local package, only the **reverse-dependency cone**
of that package rebuilds:

1. The edited package recompiles.
1. Each package that imports it (directly or transitively) recompiles.
1. The importcfg bundle and the final link derivation rebuild.

Packages outside the cone keep their existing store paths and are not
rebuilt.

To get a feel for how big the cone is in your project, see
[Benchmarking](benchmarking.md).

## Early cutoff with `contentAddressed = true`

By default, per-package derivations are input-addressed: if a package's
*inputs* change, every downstream derivation gets a new store path even if
the compiled output happens to be byte-identical.

Setting `contentAddressed = true` opts into two coupled mechanisms:

- **Floating-CA outputs.** Per-package and importcfg derivations become
  content-addressed, so a rebuild that produces a byte-identical `.a`
  resolves to the same store path and short-circuits downstream rebuilds.
- **`iface` output split.** Each per-package derivation gains a second
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

See the `contentAddressed` row of the [Builder API](builder-api.md) table
for the canonical description.

## The cost: eval time

go2nix trades build time for eval time. Every `nix build` evaluation:

1. Calls `builtins.resolveGoPackages` (the [Nix plugin](nix-plugin.md)),
   which runs `go list -json -deps` against your source tree.
1. Instantiates one derivation per package in the resulting graph.

For a large application (~3,500 packages) the warm-cache `go list` step
takes on the order of **a few hundred milliseconds**, and instantiation
adds a similar amount on top. This is the floor on every rebuild — even a
no-change rebuild — and is the main reason go2nix is overkill for small
single-binary projects.

The plugin call is impure (it reads `GOMODCACHE`), so the result is not
cached by the Nix evaluator across invocations. See
[Nix Plugin](nix-plugin.md) for details.
