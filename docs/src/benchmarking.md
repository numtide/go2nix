# Benchmarking

`bench-incremental` measures rebuild time for go2nix's default-mode builder
after touching a single file at different depths in the dependency graph.
Use it to see how `contentAddressed` and the `iface` early-cutoff behave on
a representative project before adopting them.

## Running it

```bash
nix run github:numtide/go2nix#bench-incremental -- -fixture light
```

(or `nix run .#bench-incremental -- ...` from a checkout).

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
| `-runs N` | `3` | Runs per scenario; the median is reported |
| `-scenario S` | `all` | One of `no_change`, `leaf`, `mid`, `deep`, `all` |
| `-touch-mode M` | `private` | `private` edits an unexported symbol; `exported` edits an exported one |
| `-tools L` | `nix-nocgo,nix-ca-nocgo` | Comma-separated tool variants: `nix`, `nix-ca`, `nix-nocgo`, `nix-ca-nocgo`, `nix-dynamic`, `nix-dynamic-nocgo`, `bazel` |
| `-fixture F` | `light` | `light` or `torture` (see below) |
| `-json PATH` | — | Write raw results as JSON |
| `-assert-cascade N` | — | Fail (non-zero exit) if any tool builds more than `N` derivations on a touch scenario |

The `nix-ca*` variants set `contentAddressed = true`; the `*-nocgo`
variants set `CGO_ENABLED = 0`. Comparing `nix-nocgo` against
`nix-ca-nocgo` with `-touch-mode private` shows the `iface` early-cutoff in
action.

The `nix-dynamic*` variants use the
[experimental builder](modes/experimental-mode.md)
(`buildGoApplicationExperimental` — recursive-nix + dynamic-derivations +
ca-derivations). They need a Nix that provides those experimental features
*and* the `recursive-nix` system feature in the build sandbox; the harness
probes once at startup and drops the tool with a `SKIP` notice if the probe
fails, so the rest of the run continues. Remote builders that don't advertise
`recursive-nix` will not work — this is a local-only comparison for now.

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

## Other benchmarks

The flake also exposes coarser-grained harnesses:

- `benchmark-build` — wall-clock time for a full cold build of a fixture.
- `benchmark-eval` — wall-clock time for a pure `nix eval` of the package
  graph (plugin + instantiation cost).
- `benchmark-build-cross-app-isolation` — verifies that two apps sharing
  third-party packages reuse each other's per-package store paths.
