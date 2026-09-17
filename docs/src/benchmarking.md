# Benchmarking

`bench-incremental` measures rebuild time for go2nix's default-mode builder
after touching a single file at different depths in the dependency graph.
Use it to see how `contentAddressed` and the `iface` early-cutoff behave on
a representative project before adopting them.

## Running it

```bash
nix run .#bench-incremental -- -fixture light
```

Run it from a go2nix checkout: the harness takes the fixtures, the plugin and
`lib.nix` from the git repository around the current directory (it calls
`git rev-parse --show-toplevel`), so that checkout is what gets measured.
It needs `git` and `go` on `PATH` and uses your `GOMODCACHE`; `NIXPKGS_PATH`
selects the nixpkgs it evaluates with.

The harness spins up a rooted local store
(`NIX_REMOTE=local?root=$TMPDIR/...`), loads the
[Nix plugin](nix-plugin.md) via `--option plugin-files`, does a full warm
build, then repeatedly edits one file and times the rebuild. It needs
network access (the warm build fetches modules from substituters), so it
**cannot** run inside a `nix build` sandbox — `nix flake check` only
verifies that the binary links.

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-runs N` | `3` | Runs per scenario; mean ± stddev and min..max are reported |
| `-scenario S` | `all` | One of `no_change`, `leaf`, `mid`, `deep`, `all` |
| `-touch-mode M` | `private` | `private` edits an unexported symbol; `exported` edits an exported one |
| `-tools L` | `nix-nocgo,nix-ca-nocgo` | Comma-separated tool variants: `nix`, `nix-ca`, `nix-nocgo`, `nix-ca-nocgo`, and `bazel` (torture fixture only) |
| `-fixture F` | `light` | `light` or `torture` (see below) |
| `-json PATH` | — | Write raw results as JSON |
| `-assert-cascade N` | — | Fail (non-zero exit) if any tool builds more than `N` derivations on a touch scenario |
| `-stderr-tail N` | `500` | Bytes of a failing command's stderr kept in the error message |

The `nix-ca*` variants set `contentAddressed = true`; the `*-nocgo`
variants set `CGO_ENABLED = 0`. Comparing `nix-nocgo` against
`nix-ca-nocgo` with `-touch-mode private` shows the `iface` early-cutoff in
action.

## Fixtures and scenarios

Two synthetic projects under `tests/fixtures/`:

| Fixture | Shape | `leaf` edits | `mid` edits | `deep` edits |
|---------|-------|--------------|-------------|--------------|
| `light` | small app, a few internal packages | `app/cmd/app/main.go` | `internal/handler/handler.go` | `internal/core/core.go` |
| `torture` | large app, hundreds of modules | `app-full/cmd/app-full/main.go` | `internal/aws/aws.go` | `internal/common/common.go` |

`leaf` touches the entrypoint (no reverse dependents — only the link
rebuilds). `mid` touches a package roughly halfway up the graph with a
moderate reverse-dependency cone. `deep` touches a package near the bottom
of the graph that fans out to most of the app. `no_change` measures the
eval + no-op-build floor.

## Using `-assert-cascade` in CI

```bash
nix run .#bench-incremental -- \
  -fixture light -scenario mid -touch-mode private \
  -tools nix-ca-nocgo -assert-cascade 5
```

This fails if a private-symbol edit to a mid-graph package causes more than
five derivations to rebuild — a regression check for the early-cutoff
machinery.

The repository's own CI does something else: `.github/workflows/benchmark.yml`
runs the harness with `--json` on the base branch and on the pull request and
compares them with `scripts/compare-benchmarks.py --threshold 20`, which
flags a mean time more than 20% worse or any increase in derivations built.

## Other benchmarks

The flake also exposes coarser-grained harnesses:

- `benchmark-build` — `buildGoModule` vs go2nix vs the experimental builder
  with hyperfine, over three phases: clean build, cached rebuild, rebuild
  after a source change.
- `benchmark-eval` — `nix-instantiate` time of the default builder vs the
  experimental one (plugin + instantiation cost).
- `benchmark-build-cross-app-isolation` — verifies that two apps sharing
  third-party packages reuse each other's per-package store paths.
