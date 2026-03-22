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
go2nix compile-package [flags]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--import-path` | Yes | Go import path for the package |
| `--src-dir` | Yes | Directory containing source files |
| `--output` | Yes | Output `.a` archive path |
| `--import-cfg` | Yes | Path to importcfg file |
| `--tags` | No | Comma-separated build tags |
| `--gc-flags` | No | Extra flags for `go tool compile` |
| `--trim-path` | No | Path prefix to trim |
| `--p` | No | Override `-p` flag (default: import-path) |
| `--go-version` | No | Go language version for `-lang` |
| `--pgo-profile` | No | Path to pprof CPU profile for PGO |

## compile-packages

Compile all local (non-third-party) packages in a module. Discovers the
package dependency graph and compiles in topological order.

```
go2nix compile-packages [flags] <module-root>
```

| Flag | Required | Description |
|------|----------|-------------|
| `--import-cfg` | Yes | Path to importcfg file (appended to) |
| `--out-dir` | Yes | Output directory for `.a` files |
| `--tags` | No | Comma-separated build tags |
| `--gc-flags` | No | Extra flags for `go tool compile` |
| `--trim-path` | No | Path prefix to trim |
| `--pgo-profile` | No | Path to pprof CPU profile for PGO |

## list-files

List Go source files for a package directory, respecting build tags and
constraints.

```
go2nix list-files [-tags=...] <package-dir>
```

Outputs JSON with categorized file lists (Go files, C files, assembly, etc.).

## list-packages

List all local packages in a Go module with their import dependencies.

```
go2nix list-packages [-tags=...] <module-root>
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
