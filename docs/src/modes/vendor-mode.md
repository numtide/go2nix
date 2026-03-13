# Vendor Mode

Traditional `go build` with a Nix-managed vendor directory.

## Overview

Vendor mode downloads Go modules, creates a vendor tree via symlinks, and
runs `go build -mod=vendor`. This is the simplest approach — it uses standard
Go tooling and works everywhere. Derived from
[gomod2nix](https://github.com/nix-community/gomod2nix).

The trade-off is coarser caching: a change to any dependency rebuilds the
entire application.

## Lockfile requirements

Vendor mode uses the v1-style lockfile:

```bash
go2nix generate --mode=vendor .
```

This produces a lockfile with `[mod."path@version"]` entries containing
`version`, `hash`, and optional `replaced` fields. See
[lockfile-format.md](../lockfile-format.md) for details.

## Build flow

### 1. Module filtering

The builder parses `go.mod` (via a pure-Nix parser, `parser.nix`) and filters
the lockfile to only the modules required by this specific project. This
allows sharing a single lockfile across a monorepo.

### 2. Module fetching

Each module is a fixed-output derivation that runs:

```bash
go mod download "<path>@<version>"
```

Unlike DAG mode's fetcher which produces GOMODCACHE layout, vendor mode's
`fetch.sh` produces a simpler directory tree suitable for symlinking into
a `vendor/` directory.

### 3. Vendor tree assembly

A Go utility (`symlink.go`) creates the vendor directory by symlinking
fetched modules into the correct paths. Local `replace` directives from
`go.mod` are handled by creating symlinks to the local paths.

### 4. MVS tidiness check

At build time, `mvscheck.go` validates that the vendor tree is consistent
with `go mod graph` output. This catches stale lockfiles.

### 5. Build

Standard `go build` with `-mod=vendor`:

```bash
go install -mod=vendor -trimpath ./...
```

The builder supports `subPackages`, `ldflags`, `tags`, `CGO_ENABLED`, and
all standard Go build flags.

## Usage

```nix
goEnv.buildGoApplicationVendorMode {
  src = ./.;
  goLock = ./go2nix.toml;  # Generated with --mode=vendor
  pname = "my-app";
  version = "0.1.0";
  tags = [ "nethttpomithttp2" ];
  ldflags = [ "-s" "-w" ];
  subPackages = [ "cmd/server" "cmd/cli" ];
}
```

## Directory layout

```
nix/vendor/
├── default.nix        # buildGoApplicationVendorMode
├── parser.nix         # Pure-Nix go.mod parser
├── fetch.sh           # Module fetch script
├── symlink/           # Vendor symlink utility (Go)
│   └── symlink.go
├── install/           # Dev dependency installer (Go)
│   └── install.go
└── mvscheck/          # MVS tidiness checker (Go)
    └── mvscheck.go
```

The Go utilities (`symlink.go`, `install.go`, `mvscheck.go`) are single-file
programs compiled at Nix build time using `pkgsBuildBuild.go`.

## Trade-offs

**Pros:**
- Simplest approach — uses standard `go build`
- Works with any Nix version (no experimental features)
- Smallest lockfile (no `[pkg]` section)
- Battle-tested (forked from gomod2nix)

**Cons:**
- Per-module caching only — any dependency change rebuilds everything
- Cannot take advantage of per-package parallelism
- Vendor tree reconstruction on every build

See [lockfile-format.md](../lockfile-format.md) for details on the vendor
lockfile format.
