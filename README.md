# go2nix

Nix-native Go builder that compiles at **package granularity** — the Nix
equivalent of [rules_go](https://github.com/bazel-contrib/rules_go) for Bazel.
Instead of calling `go build`, go2nix invokes `go tool compile`, `go tool asm`,
and `go tool link` directly, giving Nix full control over the dependency graph.

Each third-party Go package becomes its own Nix derivation. When a module has
50 packages but your project only imports 3, only those 3 are compiled. When a
dependency changes, only affected packages rebuild.

## Quick start

### 1. Generate a lockfile

```bash
go2nix generate .
```

This produces `go2nix.toml` — a lockfile with two sections:

- `[mod]`: module-level NAR hashes (for fixed-output fetching)
- `[pkg]`: package-level import graphs (for per-package derivations)

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

## Usage

### `buildGoApplication` parameters

| Parameter | Description | Default |
|----------------------|----------------------------------------------------------|------------|
| `src` | Source directory | (required) |
| `goLock` | Path to `go2nix.toml` lockfile | (required) |
| `pname` | Package/binary name | (required) |
| `version` | Version string | (required) |
| `subPackages` | List of sub-packages to build | `["."]` |
| `ldflags` | Linker flags | `[]` |
| `gcflags` | Compiler flags (e.g., `["-N" "-l"]` for debug) | `[]` |
| `CGO_ENABLED` | Override cgo detection | auto |
| `allowGoReference` | Allow Go SDK in output closure | `false` |
| `moduleDir` | Module directory within src | `"."` |
| `packageOverrides` | Per-package overrides (cgo deps, etc.) | `{}` |
| `nativeBuildInputs` | Extra build inputs for the final binary | `[]` |
| `meta` | Derivation metadata | `{}` |

### `mkGoEnv` parameters

| Parameter | Description | Default |
|--------------|-------------------------------------------|---------|
| `go` | Go compiler package | (required) |
| `go2nix` | go2nix CLI package | (required) |
| `callPackage`| `pkgs.callPackage` | (required) |
| `tags` | Build tags (applied to all compilations) | `[]` |
| `netrcFile` | Path to `.netrc` for private modules | `null` |

### Build tags

Pass build tags when creating the Go environment:

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  tags = [ "nethttpomithttp2" "custom_tag" ];
};
```

### Private modules

For private modules that require authentication, provide a `.netrc` file:

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  netrcFile = ./secrets/netrc;
};
```

Go's default `GOPROXY` (`https://proxy.golang.org,direct`) falls back to
direct VCS access when the proxy returns 404, so `netrcFile` is sufficient
for most private module setups. The file is copied to `$HOME/.netrc` inside
each module fetch derivation.

### Cgo and `packageOverrides`

Packages that use cgo need their C dependencies available at compile time.
Use `packageOverrides` keyed by import path or module path:

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "yubikey-agent";
  version = "0.1.6";
  packageOverrides = {
    # By import path (exact package):
    "github.com/go-piv/piv-go/piv" = {
      nativeBuildInputs = [ pkgs.pkg-config pkgs.pcsclite ];
    };
    # By module path (applies to all packages in the module):
    "github.com/diamondburned/gotk4/pkg" = {
      nativeBuildInputs = [
        pkgs.pkg-config pkgs.glib pkgs.cairo pkgs.gtk3
      ];
    };
  };
}
```

### Multiple sub-packages

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-tools";
  version = "1.0.0";
  subPackages = [ "cmd/server" "cmd/cli" ];
}
```

### Monorepo (shared lockfile)

Generate a single lockfile for all projects:

```bash
go2nix generate ./service-a ./service-b ./service-c
```

Each project references the same lockfile:

```nix
goEnv.buildGoApplication {
  src = ./service-a;
  goLock = ../go2nix.toml;   # shared
  pname = "service-a";
  version = "0.1.0";
}
```

The lockfile uses composite keys (`module@version`), so different projects
can depend on different versions of the same module without conflict.

## CLI commands

| Command | Description |
|---------------------|----------------------------------------------------------------|
| `go2nix generate` | Generate or update `go2nix.toml` from Go project directories |
| `go2nix list-files` | List source files in a package (with build constraints) |
| `go2nix list-packages` | Discover local packages in topological order |
| `go2nix compile-package` | Compile a single Go package to `.a` archive |
| `go2nix compile-packages` | Compile all local library packages (DAG-aware parallel) |
| `go2nix check` | Validate lockfile consistency with `go.mod` |

Run `go2nix generate -h` etc. for flags.

## Development

### Prerequisites

- [Nix](https://nixos.org/download/) with flakes enabled
- [direnv](https://direnv.net/) (recommended)

### Setup

```bash
git clone https://github.com/numtide/go2nix
cd go2nix
direnv allow   # or: nix develop
```

The dev shell (via `flake.nix` + `devshell.nix`) provides:

- `go` compiler
- `$NIX_PATH` set to the flake's nixpkgs
- `scripts/` and `bin/` on `$PATH`

Per-user overrides can be added to `.envrc.local` (gitignored).

### Project layout

```
go2nix/
  go/go2nix/            # Go CLI source
    cmd/go2nix/         #   CLI entry points
    pkg/compile/        #   Package compilation (compile, asm, cgo)
    pkg/gofiles/        #   Source file discovery + embed resolution
    pkg/localpkgs/      #   Local package discovery + topo sort
    pkg/lockfile/       #   Lockfile read/write
    pkg/lockfilegen/    #   Lockfile generation (go list + NAR hash)
    pkg/mvscheck/       #   go.mod tidiness checks
    package.nix         #   Bootstrap build (uses buildGoModule)
  nix/                  # Nix library
    mk-go-env.nix       #   Entry point: creates Go toolchain scope
    scope.nix            #   Self-referential package set (lib.makeScope)
    stdlib.nix           #   Compile Go standard library
    fetch-go-module.nix  #   Fixed-output derivation for module fetching
    build-go-application.nix  # Main build function
    helpers.nix          #   Pure Nix utility functions
    hooks/               #   Setup hooks (compile-go-pkg.sh, link-go-binary.sh)
  tests/
    default.nix          #   nix-build test entry point
    packages/            #   Test projects (yubikey-agent, dotool, nwg-drawer)
    nix/                 #   Nix unit tests (nix eval -f tests/nix/helpers_test.nix)
  docs/
    go2nix-architecture.md  # Technical architecture reference
  lib.nix               # Flake-level re-exports
  flake.nix             # Nix flake
```

### Building go2nix itself

go2nix is bootstrapped with `buildGoModule` (it can't use `buildGoApplication`
since that depends on go2nix):

```bash
nix build .#go2nix
```

### Running Go tests

```bash
cd go/go2nix
go test ./pkg/... -short
```

### Running Nix integration tests

Build all test projects:

```bash
nix-build tests/
```

Build a specific test:

```bash
nix-build tests/ -A dotool
nix-build tests/ -A yubikey-agent
nix-build tests/ -A nwg-drawer
```

Run Nix unit tests (pure eval, no build):

```bash
nix eval -f tests/nix/helpers_test.nix
```

### Formatting

The project uses [treefmt](https://github.com/numtide/treefmt) with:

- **Nix**: nixfmt, deadnix, statix
- **Shell**: shellcheck, shfmt
- **Markdown**: mdformat
- **Go**: gofumpt

```bash
nix fmt
```

### Debugging

Set `GO2NIX_DEBUG=1` for verbose output from the go2nix CLI:

```bash
GO2NIX_DEBUG=1 go2nix compile-package --import-path foo --src-dir ./foo --output foo.a --importcfg importcfg
```

## How it works

go2nix replaces `go build` with direct tool invocations:

```
go2nix.toml ──(Nix eval)──> per-package derivations
                                    │
                              [stdlib.nix]  ──> go install std ──> stdlib .a files
                              [fetch FODs]  ──> go mod download ──> module sources
                                    │
                              [compile-go-pkg.sh]
                                    │  go2nix compile-package
                                    v
                              per-package .a archives
                                    │
                              [link-go-binary.sh]
                                    │  go2nix compile-packages (local libs, parallel)
                                    │  go2nix compile-package (main package)
                                    │  go tool link
                                    v
                                  binary
```

See [docs/go2nix-architecture.md](docs/go2nix-architecture.md) for the full
technical reference.

## License

TBD
