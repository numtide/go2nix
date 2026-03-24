# go2nix

> **⚠️ Experimental** — APIs and lockfile formats may change without notice.

Nix-native Go builder with per-package derivations and fine-grained caching.

## Why go2nix?

go2nix is for projects that want more visibility and reuse than the usual
"fetch all modules, then build everything in one derivation" model.

In practice that means:

- the lockfile pins **modules**, not the package graph
- the builder discovers the **package graph** and compiles it at package granularity
- Nix can cache and rebuild **individual Go packages**, not just the whole app
- local packages, third-party packages, and test-only packages can be modeled
  separately in default mode

This tends to work especially well for monorepos and multi-package repositories
that want to maximize Nix store reuse. When only part of the Go package graph
changes, go2nix can often reuse the rest of the graph instead of rebuilding the
whole application derivation.

The approach is architecturally inspired by Bazel's `rules_go`: both systems
work from an explicit package graph instead of treating `go build` as a black
box. The difference is scope — `rules_go` is a full Bazel rule ecosystem,
while go2nix is a Nix-native builder with a much narrower goal. It does not
aim to replicate toolchain transitions, proto rules, or the full feature
surface of `rules_go`.

If you just want the simplest way to package a Go program in nixpkgs,
`buildGoModule` is still the default choice. go2nix is aimed at cases where
per-package reuse and explicit graph handling are worth the extra machinery.

See [Architecture](docs/src/go2nix-architecture.md) for how the builder works
and [Builder API](docs/src/builder-api.md) for the full attribute reference.

## Quick start

### 1. Generate a lockfile

```bash
go2nix generate .
```

`generate` is also the default command, so `go2nix .` works as well.

### 2. Add go2nix to your flake

```nix
{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
    go2nix = {
      url = "github:numtide/go2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { nixpkgs, go2nix, ... }:
  let
    system = "x86_64-linux";
    pkgs = nixpkgs.legacyPackages.${system};
    goEnv = go2nix.lib.mkGoEnv {
      inherit (pkgs) go callPackage;
      go2nix = go2nix.packages.${system}.go2nix;
    };
  in {
    packages.${system}.default = goEnv.buildGoApplication {
      src = ./.;
      goLock = ./go2nix.toml;
      pname = "my-app";
      version = "0.1.0";
    };
  };
}
```

### 3. Build

```bash
nix build
```

## Builder modes

| Mode | How it works | Requires |
|------|-------------|----------|
| **Default** | `go tool compile/link` per-package | [go2nix-nix-plugin](packages/go2nix-nix-plugin/) — Nix plugin providing `builtins.resolveGoPackages` |
| **Experimental** | Recursive-nix at build time | Nix >= 2.34 with `recursive-nix`, `ca-derivations`, `dynamic-derivations` |

```nix
# Default (recommended):
goEnv.buildGoApplication { ... }

# Experimental (requires nix >= 2.34 with experimental features):
goEnv.buildGoApplicationExperimental { ... }
```

See [Default Mode](docs/src/modes/default-mode.md) and
[Experimental Mode](docs/src/modes/experimental-mode.md) for details.

## CLI commands

| Command | Description |
|---------------------|----------------------------------------------------------------|
| `go2nix generate` | Generate `go2nix.toml` lockfile |
| `go2nix check` | Validate lockfile against `go.mod` |

See [CLI Reference](docs/src/cli-reference.md) for all commands and flags.

## Comparison with Nix alternatives

| Tool | Main model | Best at | Tradeoff vs go2nix |
|------|------------|---------|--------------------|
| `buildGoModule` | One fetch derivation + one main build derivation | Standard nixpkgs packaging, lowest conceptual overhead | Coarser caching; Nix does not model the Go package graph |
| `gomod2nix` | Lock modules, then build with `go build` in a conventional app derivation | Offline reproducible app builds with a mature lockfile workflow | Still largely app-level/module-level, not package-graph-level |
| `gobuild.nix` | Per-module derivations backed by `GOCACHEPROG` | Incremental module builds and package-set style composition | Granularity is centered on modules and Go cache subsets, not direct per-package derivations |
| `nix-gocacheprog` | Reuse Go's own cache through a host daemon and sandbox hole | Fast local development on one machine | Intentionally impure; optimization layer, not a pure package-graph builder |
| `go2nix` | Discover package graph and compile with `go tool compile/link` per package | Fine-grained Nix caching and explicit package-level rebuilds | More moving parts; default mode needs the plugin |

### In one sentence

- Choose `buildGoModule` when you want the standard nixpkgs path.
- Choose `gomod2nix` when you want an offline lockfile-driven app build.
- Choose `nix-gocacheprog` when you want faster local iteration and accept impurity.
- Choose `gobuild.nix` when per-module package-set composition is the goal.
- Choose `go2nix` when you want Nix to understand and cache the Go build at
  package granularity.

## Repository layout

```
go/go2nix/       Go CLI (generate, compile, resolve, test runner)
nix/             Nix builders (dag/ for default mode, dynamic/ for experimental)
packages/        Nix package definitions (go2nix, nix-plugin, test fixtures)
tests/           Integration test fixtures and harnesses
docs/            mdBook documentation
```

## Development

```bash
git clone https://github.com/numtide/go2nix
cd go2nix
direnv allow   # or: nix develop
```

```bash
cd go/go2nix && go test ./...               # Go unit tests
nix build .#test-dag-fixture-testify-basic  # Nix integration test (one fixture)
nix fmt                                     # format all files
```

## License

TBD
