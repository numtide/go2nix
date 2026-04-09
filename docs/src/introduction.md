# Introduction

> **⚠️ Experimental** — APIs and lockfile formats may change without notice.

go2nix is a Nix-native Go builder with per-package derivations and
fine-grained caching. It is an alternative to nixpkgs `buildGoModule` for
projects that want more visibility and reuse than the usual "fetch all
modules, then build everything in one derivation" model.

In Go, a *module* is the versioned unit you depend on (one `go.mod`, one
entry in `go.sum`); a *package* is a single importable directory of `.go`
files. One module typically contains many packages. go2nix locks modules but
builds packages:

- the lockfile pins **modules**, not the package graph
- the builder discovers the **package graph** and compiles it at package granularity
- Nix can cache and rebuild **individual Go packages**, not just the whole app

This works especially well for monorepos and multi-package repositories that
want to maximize Nix store reuse. When only part of the Go package graph
changes, go2nix reuses the rest of the graph instead of rebuilding the whole
application derivation.

If you just want the simplest way to package a Go program in nixpkgs,
`buildGoModule` is still the default choice. go2nix is aimed at cases where
per-package reuse and explicit graph handling are worth the extra machinery.

## Quick start

> **Heads up:** the default builder requires the go2nix [Nix plugin](nix-plugin.md)
> to be loaded into your evaluator. Without it, `nix build` fails with
> `error: attribute 'resolveGoPackages' missing`.

### 1. Generate a lockfile

```bash
go2nix generate .
```

This writes a `go2nix.toml` next to your `go.mod` — one NAR hash per module.
See [Lockfile Format](lockfile-format.md).

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

Default mode needs the [Nix plugin](nix-plugin.md) loaded in the evaluator:

```bash
nix build \
  --option plugin-files \
  "$(nix build --no-link --print-out-paths github:numtide/go2nix#go2nix-nix-plugin)/lib/nix/plugins/libgo2nix_plugin.so"
```

For permanent setup, see [Nix Plugin → Loading the plugin](nix-plugin.md#loading-the-plugin).

## Where to next

- [Architecture](go2nix-architecture.md) — how the builder works
- [Builder Modes](modes/README.md) — default vs experimental
- [Incremental Builds](incremental-builds.md) — what gets cached
- [Builder API](builder-api.md) — full attribute reference
- [Troubleshooting](troubleshooting.md) — when something doesn't work
