# Builder Modes

A Go application is made up of *modules* (downloaded units, each with a
`go.mod`) and *packages* (individual directories of `.go` files within a
module). A single module can contain dozens of packages. The two builder
modes differ in what unit of work becomes a Nix derivation, which determines
rebuild granularity when a dependency changes.

| Mode | How it works | Lockfile | Caching | Nix features |
|------|-------------|----------|---------|--------------|
| **[Default](default-mode.md)** | `go tool compile/link` per-package | `[mod]` + optional `[replace]` | Per-package | go2nix-nix-plugin |
| **[Experimental](experimental-mode.md)** | Recursive-nix at build time | `[mod]` + optional `[replace]` | Per-package | `dynamic-derivations`, `ca-derivations`, `recursive-nix` |

- **Default** (`buildGoApplication`): every *package* (not just every module)
  gets its own derivation. go2nix calls `go tool compile` and `go tool link`
  directly, bypassing `go build`. The import graph is discovered at eval time
  by the go2nix-nix-plugin (`builtins.resolveGoPackages`), so the lockfile
  stays small (`[mod]` hashes plus optional `[replace]`). When one package
  changes, only it and its reverse dependencies rebuild.

- **Experimental** (`buildGoApplicationExperimental`): same per-package
  granularity as the default mode, but discovers the import graph at *build
  time* inside a recursive-nix wrapper instead of at eval time. The lockfile
  stays small (`[mod]` plus optional `[replace]`) and only changes when module
  resolution changes. Requires Nix >= 2.34 with experimental features enabled.

## Choosing a mode

Use `buildGoApplication` (the default) for the best balance of caching and
simplicity — the lockfile is small (just module hashes), and the
go2nix-nix-plugin resolves the package graph at eval time.

Use `buildGoApplicationExperimental` only if you have Nix >= 2.34 with
`dynamic-derivations`, `ca-derivations`, and `recursive-nix` enabled, and
want per-package caching without requiring the plugin.

```nix
# Default (recommended):
goEnv.buildGoApplication { ... }

# Experimental (requires nix experimental features):
goEnv.buildGoApplicationExperimental { ... }
```
