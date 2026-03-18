# Compilation Pipeline

How go2nix compiles Go code at package granularity, bypassing `go build`.

## Overview

DAG and dynamic modes call `go tool compile` and `go tool link` directly
instead of `go build`. This gives Nix full control over the dependency graph
at package granularity — each package becomes its own derivation with its own
cache key.

The pipeline has three stages:

1. **Standard library** — compiled once, shared across all builds
1. **Third-party packages** — one derivation per package, compiled in
   dependency order
1. **Local packages + link** — compiled and linked in the final derivation

## Standard library (stdlib.nix)

A single derivation compiles all stdlib packages:

```bash
GODEBUG=installgoroot=all GOROOT=. go install -v --trimpath std
```

Output layout:

```
$out/
├── fmt.a
├── net/http.a
├── ...
└── importcfg        # packagefile fmt=$out/fmt.a ...
```

The `importcfg` file maps import paths to `.a` file paths. All downstream
compilations start by including this file.

## importcfg threading

Every Go compilation requires an `importcfg` file listing where to find
dependency archives. go2nix threads this through the derivation graph:

1. stdlib produces `$out/importcfg` with all stdlib entries
1. Each third-party package produces `$out/importcfg` with one line:
   `packagefile <import-path>=$out/<import-path>.a`
1. At compile time, `importcfg` is assembled by concatenating stdlib's
   file plus all dependency packages' files

This is implemented in `compile-go-pkg.sh`:

```bash
cat @stdlib@/importcfg > $NIX_BUILD_TOP/importcfg
for dep in $buildInputs; do
  if [ -f "$dep/importcfg" ]; then
    cat "$dep/importcfg" >> $NIX_BUILD_TOP/importcfg
  fi
done
```

## Third-party package compilation

Each third-party package derivation uses the `goModuleHook` setup hook
(`compile-go-pkg.sh`). The hook:

1. Assembles `importcfg` from stdlib + dependency packages
1. Calls `go2nix compile-package` to compile the package
1. Writes `$out/<import-path>.a` (the archive) and `$out/importcfg`

The `go2nix compile-package` command:

- Lists source files via `go/build` (respecting build tags and constraints)
- Handles `.go`, `.c`, `.s` (assembly), and cgo files
- For cgo: runs `cgo` tool, compiles C code with `$CC`, merges objects
- For assembly: runs `go tool asm`
- Calls `go tool compile` with the assembled importcfg
- Produces a single `.a` archive via `go tool pack`

## Local package compilation

The final application derivation uses the `goAppHook` setup hook
(`link-go-binary.sh`). This hook:

1. Validates lockfile consistency (`go2nix check --lockfile`)
1. Assembles importcfg from stdlib + all third-party deps
1. Compiles all local library packages in topological order
   (`go2nix compile-packages`)
1. For each sub-package in `subPackages`:
   - Compiles the `main` package
   - Links with `go tool link` to produce a binary

## Linking

The link step uses `go tool link`:

```bash
go tool link \
  -buildid=redacted \
  -importcfg $NIX_BUILD_TOP/importcfg \
  ${goLdflags:-} \
  $linkflags \
  -o $out/bin/$binname \
  $localdir/$importpath.a
```

When cgo is detected (`.has_cgo` marker), external linking mode is used:
`-extld $CC -linkmode external`.

## DAG scheduling

In DAG mode, Nix handles scheduling: each package derivation declares its
dependencies via `buildInputs`, and Nix builds them in the correct order
with maximal parallelism.

In dynamic mode, the `resolve` command discovers the graph at build time
and creates CA derivations via `nix derivation add`. The Nix daemon then
schedules these derivations the same way.

For local packages, `go2nix compile-packages` uses an internal topological
sort to compile library packages before the packages that import them.
