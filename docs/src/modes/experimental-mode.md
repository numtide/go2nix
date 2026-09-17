# Experimental Mode

Per-package CA derivations at build time, via recursive-nix.

## Overview

The experimental mode moves package graph discovery from Nix eval time to build
time. A single recursive-nix wrapper derivation runs `go2nix resolve`, which
calls `go list -json -deps` to discover the import graph, then registers one
content-addressed (CA) derivation per package with the Nix daemon (over its
socket; `nix derivation add` is the fallback when no daemon is reachable). The
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

`mkGoEnv` must also be given `nixPackage` — the Nix that runs inside the
wrapper derivation, and the one the ">= 2.34" check is made against;
`buildGoApplicationExperimental` throws without it. The wrapper asks for the
`recursive-nix` system feature, so the machine that builds it has to offer
it.

What this builder does not do (none of it is an error, the attributes are
simply ignored): it runs no tests (`doCheck`, `checkFlags`), has no
lockfile-free mode (`goLock` is required), and ignores `version`, `meta`,
`goProxy`, `allowGoReference`, `contentAddressed`, `extraMainSrcFiles`,
`srcFilter`, `env` and `passthru`. `CGO_ENABLED` does not default to the
scope's `goEnv.CGO_ENABLED`, and the scope's `goEnv` reaches the standard
library and `go2nix resolve` but not the per-package derivations. The whole
`src` is an input of the wrapper, so any change to it re-runs
`go2nix resolve`, even though the per-package derivations it registers are
then reused.

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
sandbox. Each FOD runs `go mod download` and keeps the module's extracted
source tree, the same output as the default mode's fetcher; `go2nix resolve`
then assembles a module cache from them. The `netrcFile` option (an argument
of `mkGoEnv`) supports private module authentication; like any input of a
fixed-output derivation it ends up in the store, see
[Private modules](../builder-api.md#private-modules-netrcfile).

### 3. Package graph discovery (build time)

With all modules available, `go list -json -deps` discovers the full import
graph. The default mode performs this step at eval time via the
go2nix-nix-plugin — the experimental mode defers it to build time inside the
recursive-nix sandbox.

### 4. CA derivation registration (build time)

For each package, `go2nix resolve` registers a content-addressed derivation that compiles one Go package to an archive
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

`packageOverrides` is serialized to JSON and passed to `go2nix resolve`,
which adds the extra inputs to the appropriate CA derivations. Only
`nativeBuildInputs` is forwarded; `env` (and any other key) is rejected at
eval time because derivations are synthesized at build time and only store
paths can cross that boundary. See
[Package Overrides](../package-overrides.md) for lookup rules and recipes.

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

## Trade-offs (vs default mode)

**Pros:**

- No Nix plugin required — package graph discovery happens in a hermetic
  build, not via an impure eval-time `go list`
- CA deduplication is always on — comment-only edits don't trigger rebuilds
- Same small lockfile and per-package caching as default mode

**Cons:**

- Requires Nix >= 2.34 with experimental features (`recursive-nix`,
  `ca-derivations`, `dynamic-derivations`)
- Build-time overhead from derivation registration: `.drv` paths are
  computed in-process (microseconds each) and registered concurrently over
  the nix-daemon socket; the per-derivation `nix derivation add` subprocess
  is only used as a fallback when no daemon is reachable

Performance and scaling characteristics depend on recursive-nix support,
content-addressed derivations, and daemon round-trip latency.
