# Builder Modes

A Go application is made up of *modules* (downloaded units, each with a
`go.mod`) and *packages* (individual directories of `.go` files within a
module). A single module can contain dozens of packages. The two builder
modes differ in what unit of work becomes a Nix derivation, which determines
rebuild granularity when a dependency changes.

| Mode | How it works | Lockfile | Caching | Nix features |
|------|-------------|----------|---------|--------------|
| **[DAG](dag-mode.md)** | `go tool compile/link` per-package | `[mod]` + optional `[replace]` | Per-package | go-nix-plugin |
| **[Dynamic](dynamic-mode.md)** | Recursive-nix, DAG at build time | `[mod]` + optional `[replace]` | Per-package | `dynamic-derivations`, `ca-derivations`, `recursive-nix` |

- **DAG** goes deeper: every *package* (not just every module) gets its own
  derivation. go2nix calls `go tool compile` and `go tool link` directly,
  bypassing `go build`. The import graph is discovered at eval time by the
  go-nix-plugin (`builtins.resolveGoPackages`), so the lockfile stays small
  (`[mod]` hashes plus optional `[replace]`). When one package changes, only
  it and its reverse dependencies rebuild.

- **Dynamic** achieves the same per-package granularity as DAG, but discovers
  the import graph at *build time* inside a recursive-nix wrapper instead of
  recording it in the lockfile. The lockfile stays small (`[mod]` plus
  optional `[replace]`) and only changes when module resolution changes.
  Requires Nix >= 2.34 with experimental features enabled.

## Choosing a mode

Start with **DAG** for the best balance of caching and simplicity — the
lockfile is small (just module hashes), and the go-nix-plugin resolves the
package graph at eval time. Use **Dynamic** if you have a compatible Nix
version and want per-package caching without requiring the plugin.

`buildGoApplication` auto-selects: dynamic mode when `builtins.outputOf` is
available and `nixPackage` is set, otherwise DAG mode. Use the explicit
builders to override:

```nix
goEnv.buildGoApplicationDAGMode { ... }
goEnv.buildGoApplicationDynamicMode { ... }
```
