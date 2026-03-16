# go2nix

Nix-native Go builder with two packaging modes — per-package derivations with
fine-grained caching.

## Builder modes

| Mode | How it works | Lockfile | Caching |
|------|-------------|----------|---------|
| **DAG** | `go tool compile/link` per-package | `[mod]` only | Per-package |
| **Dynamic** | Recursive-nix, DAG at build time | `[mod]` only | Per-package |

See [docs/src/go2nix-architecture.md](docs/src/go2nix-architecture.md) for details on
each mode.

## Quick start

### 1. Generate a lockfile

```bash
go2nix generate .
```

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

### Choosing a mode

```nix
# Auto-select (dynamic if available, otherwise DAG):
goEnv.buildGoApplication { ... }

# Explicitly use a specific mode:
goEnv.buildGoApplicationDAGMode { ... }
goEnv.buildGoApplicationDynamicMode { ... }
```

## CLI commands

| Command | Description |
|---------------------|----------------------------------------------------------------|
| `go2nix generate` | Generate `go2nix.toml` lockfile |
| `go2nix generate --mode=dynamic` | Lockfile with `[mod]` only (no `[pkg]`) |
| `go2nix check` | Validate lockfile against `go.mod` |

Run `go2nix generate -h` for all flags.

## Documentation

- [Architecture](docs/src/go2nix-architecture.md) — builder modes, lockfile format,
  compilation pipeline, package DAG
- [Dynamic derivations](docs/src/internals/dynamic-derivations.md) — recursive-nix + CA
  derivations mode

## Development

```bash
git clone https://github.com/numtide/go2nix
cd go2nix
direnv allow   # or: nix develop
```

```bash
go test ./pkg/...      # Go tests
nix-build tests/       # Nix integration tests
nix fmt                # format all files
```

## License

TBD
