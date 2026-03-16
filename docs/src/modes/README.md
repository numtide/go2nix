# Builder Modes

A Go application is made up of *modules* (downloaded units, each with a
`go.mod`) and *packages* (individual directories of `.go` files within a
module). A single module can contain dozens of packages. The three builder
modes differ in what unit of work becomes a Nix derivation, which determines
rebuild granularity when a dependency changes.

| Mode | How it works | Lockfile | Caching | Nix features |
|------|-------------|----------|---------|--------------|
| **[Vendor](vendor-mode.md)** | `go build` with vendored deps | `[mod]` only | Per-module | None |
| **[DAG](dag-mode.md)** | `go tool compile/link` per-package | `[mod]` only | Per-package | go-nix-plugin |
| **[Dynamic](dynamic-mode.md)** | Recursive-nix, DAG at build time | `[mod]` only | Per-package | `dynamic-derivations`, `ca-derivations`, `recursive-nix` |

- **Vendor** wraps the standard `go build` with a Nix-managed vendor directory.
  Each module is fetched as a fixed-output derivation, then all sources are
  combined and compiled in a single build step. Changing any dependency
  rebuilds everything.

- **DAG** goes deeper: every *package* (not just every module) gets its own
  derivation. go2nix calls `go tool compile` and `go tool link` directly,
  bypassing `go build`. The import graph is discovered at eval time by the
  go-nix-plugin (`builtins.resolveGoPackages`), so the lockfile stays small
  (just `[mod]` hashes). When one package changes, only it and its reverse
  dependencies rebuild.

- **Dynamic** achieves the same per-package granularity as DAG, but discovers
  the import graph at *build time* inside a recursive-nix wrapper instead of
  recording it in the lockfile. The lockfile stays small (`[mod]` only) and
  only changes when `go.mod` changes. Requires Nix >= 2.34 with experimental
  features enabled.

## Choosing a mode

Start with **Vendor** if you want the simplest setup and don't need
fine-grained caching. Move to **DAG** when rebuild times matter — the lockfile
is the same size as vendor/dynamic (just module hashes), but requires the
go-nix-plugin. Use **Dynamic** if you have a compatible Nix version and want
per-package caching without requiring the plugin.

`buildGoApplication` auto-selects: dynamic mode when `builtins.outputOf` is
available and `nixPackage` is set, otherwise DAG mode. Use the explicit
builders to override:

```nix
goEnv.buildGoApplicationVendorMode { ... }
goEnv.buildGoApplicationDAGMode { ... }
goEnv.buildGoApplicationDynamicMode { ... }
```
