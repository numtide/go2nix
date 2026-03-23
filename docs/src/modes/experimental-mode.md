# Experimental Mode

Per-package CA derivations at build time, via recursive-nix.

## Overview

The experimental mode moves package graph discovery from Nix eval time to build
time. A single recursive-nix wrapper derivation runs `go2nix resolve`, which
calls `go list -json -deps` to discover the import graph, then registers one
content-addressed (CA) derivation per package via `nix derivation add`. The
wrapper's output is a `.drv` file; `builtins.outputOf` resolves it to the
final binary at eval time.

Because derivations are content-addressed, a change that doesn't affect the
compiled output (e.g., editing a comment) won't propagate rebuilds — Nix
deduplicates by content hash.

## Requirements

The experimental mode requires Nix >= 2.34 with these experimental features
enabled:

```
extra-experimental-features = recursive-nix ca-derivations dynamic-derivations
```

## Lockfile requirements

The experimental mode uses the same lockfile format as the default mode:

```bash
go2nix generate .
```

The package graph is discovered at build time, so the lockfile does not store
package-level dependency data. It contains `[mod]` hashes and optional
`[replace]` entries. See [lockfile-format.md](../lockfile-format.md) for
details.

## Build flow

### 1. Wrapper derivation (eval time)

Nix evaluates a text-mode CA derivation (`${pname}.drv`) that will run
`go2nix resolve` at build time. All inputs (Go toolchain, go2nix, Nix binary,
source, lockfile) are captured as derivation inputs.

### 2. Module FODs (build time)

`go2nix resolve` reads `[mod]` from the lockfile and creates fixed-output
derivations for each module, then builds them inside the recursive-nix
sandbox. Each FOD runs `go mod download` and produces a GOMODCACHE directory.
The `netrcFile` option supports private module authentication.

### 3. Package graph discovery (build time)

With all modules available, `go list -json -deps` discovers the full import
graph. The default mode performs this step at eval time via the
go2nix-nix-plugin — the experimental mode defers it to build time inside the
recursive-nix sandbox.

### 4. CA derivation registration (build time)

For each package, `go2nix resolve` calls `nix derivation add` to register a
content-addressed derivation that compiles one Go package to an archive
(`.a` file). Dependencies between packages are expressed as derivation inputs.
Local packages are also individual CA derivations.

### 5. Link derivation (build time)

A final CA derivation links all compiled packages into the output binary.
For multi-binary projects, a collector derivation aggregates multiple link
outputs.

### 6. Output resolution (eval time)

The wrapper's output is the `.drv` file path. `builtins.outputOf` tells Nix
to build that derivation and use its output, connecting eval time to the
build-time-generated derivation graph.

## Package overrides

Per-package customization (e.g., for cgo libraries):

```nix
goEnv.buildGoApplicationExperimental {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  packageOverrides = {
    "github.com/mattn/go-sqlite3" = {
      nativeBuildInputs = [ pkg-config sqlite ];
    };
  };
}
```

Overrides are serialized to JSON and passed to `go2nix resolve`, which adds
the extra inputs to the appropriate CA derivations.

**Note:** The experimental builder only supports `nativeBuildInputs` in
`packageOverrides`. The `env` attribute supported by the default builder is
not available here because derivations are synthesized at build time by
`go2nix resolve`. Unknown attributes are rejected at eval time.

## Usage

```nix
goEnv.buildGoApplicationExperimental {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  subPackages = [ "cmd/server" ];
  tags = [ "nethttpomithttp2" ];
  ldflags = [ "-s" "-w" ];
}
```

The result has a `target` passthru attribute containing the final binary,
resolved via `builtins.outputOf`.

## Directory layout

```
nix/dynamic/
└── default.nix    # buildGoApplicationExperimental (wrapper derivation)
```

The build-time logic lives in the `go2nix resolve` command
(see [cli-reference.md](../cli-reference.md)).

## Trade-offs

**Pros:**

- Small lockfile — only `[mod]` hashes (same as default mode)
- No lockfile regeneration when import graph changes (only when modules change)
- Per-package caching via CA derivations
- CA deduplication — comment-only edits don't trigger rebuilds

**Cons:**

- Requires Nix >= 2.34 with experimental features (`recursive-nix`,
  `ca-derivations`, `dynamic-derivations`)
- Build-time overhead from `nix derivation add` calls (~32ms each)
- Parallelism limited by SQLite write lock (saturates at ~4 concurrent adds)

Performance and scaling characteristics depend on recursive-nix support,
content-addressed derivations, and the overhead of `nix derivation add`.
