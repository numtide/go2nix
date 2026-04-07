# CLI Reference

All commands are subcommands of `go2nix`. Set `GO2NIX_DEBUG=1` for verbose
output.

## generate

Generate a lockfile from one or more Go module directories.

```
go2nix generate [flags] [dir...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-o` | `go2nix.toml` | Output lockfile path |
| `-j` | `NumCPU` | Max parallel hash invocations |

When no directory is given, defaults to `.`. Multiple directories produce a
merged lockfile (monorepo support).

The generated lockfile is shared by both builder modes. Use
`buildGoApplication` (default) or `buildGoApplicationExperimental` in Nix.

Examples:

```bash
go2nix generate .          # Write go2nix.toml in the current module
go2nix .                   # Same as generate: default command
go2nix generate -o lock.toml ./a ./b
```

## check

Validate a lockfile against `go.mod`.

```
go2nix check [flags] [dir]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--lockfile` | `go2nix.toml` | Path to lockfile for consistency check |

Verifies that all `go.mod` requirements are present in the lockfile with
correct versions.

## compile-package

Compile a single Go package to an archive (`.a` file). Used internally by
the default mode's setup hooks.

```
go2nix compile-package --manifest FILE --import-path PATH --src-dir DIR --output FILE [flags]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--manifest` | Yes | Path to compile-manifest.json |
| `--import-path` | Yes | Go import path for the package |
| `--src-dir` | Yes | Directory containing source files |
| `--output` | Yes | Output `.a` archive path |
| `--importcfg-output` | No | Write importcfg entry for consumers to this path |
| `--trim-path` | No | Path prefix to trim |
| `--p` | No | Override `-p` flag (default: import-path) |
| `--go-version` | No | Go language version for `-lang` |

## list-files

List Go source files for a package directory, respecting build tags and
constraints.

```
go2nix list-files [-tags=...] [-go-version=...] <package-dir>
```

Outputs JSON with categorized file lists (Go files, C files, assembly, etc.).

`-go-version` sets the target Go toolchain version (e.g. `1.25`) used to
evaluate `//go:build go1.N` constraints; defaults to `go env GOVERSION`.

## list-packages

List all local packages in a Go module with their import dependencies.

```
go2nix list-packages [-tags=...] [-go-version=...] <module-root>
```

Outputs JSON with each package's import path and dependencies.

## resolve

Build-time command for dynamic mode. Discovers the package graph, creates
CA derivations via `nix derivation add`, and produces a `.drv` file as
output.

```
go2nix resolve [flags]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--src` | Yes | Store path to Go source |
| `--mod-root` | No | Subdirectory within `src` containing `go.mod` |
| `--lockfile` | Yes | Path to go2nix.toml lockfile |
| `--system` | Yes | Nix system (e.g., `x86_64-linux`) |
| `--go` | Yes | Path to go binary |
| `--nix` | Yes | Path to nix binary |
| `--pname` | Yes | Output binary name |
| `--output` | Yes | `$out` path |
| `--stdlib` | Yes | Path to pre-compiled Go stdlib |
| `--go2nix` | No | Path to go2nix binary (defaults to self) |
| `--bash` | No | Path to bash binary |
| `--coreutils` | No | Path to a coreutils binary (e.g., `coreutils/bin/mkdir`) |
| `--sub-packages` | No | Comma-separated sub-packages |
| `--tags` | No | Comma-separated build tags |
| `--ldflags` | No | Linker flags |
| `--cgo-enabled` | No | Override CGO_ENABLED (0 or 1) |
| `--gcflags` | No | Extra flags for go tool compile |
| `--pgo-profile` | No | Store path to pprof CPU profile for PGO |
| `--overrides` | No | JSON-encoded packageOverrides |
| `--cacert` | No | Path to CA certificate bundle |
| `--netrc-file` | No | Path to .netrc for private modules |
| `--nix-jobs` | No | Max concurrent `nix derivation add` calls |

This command is not intended for direct use — it is invoked by the dynamic
mode Nix builder inside a recursive-nix build.

## build-modinfo

Generate a `modinfo` linker directive for embedding `debug/buildinfo`
metadata into the final binary. This is a standalone utility; the default
mode's `link-binary` command generates modinfo internally.

```
go2nix build-modinfo [flags] <module-root>
```

| Flag | Required | Description |
|------|----------|-------------|
| `--lockfile` | Yes | Path to go2nix.toml lockfile |
| `--go` | No | Path to go binary (default: from PATH) |

Outputs a `modinfo` directive for the linker's importcfg (embedding
`debug/buildinfo` metadata), and optionally a `godebug` line with the
default GODEBUG value parsed from the module's `go.mod` (used for
`-X=runtime.godebugDefault=...`).

## generate-test-main

Generate a `_testmain.go` file that registers test, benchmark, fuzz, and
example functions. Used internally by the test runner.

```
go2nix generate-test-main [flags]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--import-path` | Yes | Import path of the package under test |
| `--test-files` | No | Comma-separated absolute paths to internal `_test.go` files |
| `--xtest-files` | No | Comma-separated absolute paths to external `_test.go` files |
| `--output` | No | Output file path (default: stdout) |

## test-packages

Compile and run tests for all testable local packages in a module. Used
internally by the default mode's check phase.

```
go2nix test-packages --manifest FILE
```

| Flag | Required | Description |
|------|----------|-------------|
| `--manifest` | Yes | Path to test-manifest.json |

Discovers local packages with `_test.go` files, compiles internal and
external test archives, generates test mains, links test binaries, and
runs them. See [test-support.md](test-support.md) for details on the
test pipeline.

## link-binary

Link Go application binaries. Reads a link manifest that declares all
inputs (importcfg parts, local archives, ldflags, etc.), validates the
lockfile, generates modinfo, compiles main packages, and invokes the
linker. Used internally by the default mode's build phase.

```
go2nix link-binary --manifest FILE --output DIR
```

| Flag | Required | Description |
|------|----------|-------------|
| `--manifest` | Yes | Path to link-manifest.json |
| `--output` | Yes | Output directory (binaries written to `<output>/bin/`) |
