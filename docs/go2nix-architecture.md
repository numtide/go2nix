# go2nix Internals

Technical reference for [`go2nix/`](../go2nix/) — a composite-key variant of
gomod2nix developed at Anthropic for monorepo use, staged here as a benchmark
case and potential upstream contribution.

## Table of contents

- [Project overview](#project-overview)
- [Relationship to gomod2nix](#relationship-to-gomod2nix)
- [The core idea: composite keys](#the-core-idea-composite-keys)
- [The Go CLI](#the-go-cli)
  - [Module collection](#module-collection)
  - [Caching](#caching)
  - [NAR hashing](#nar-hashing)
- [The Nix builder](#the-nix-builder)
  - [Per-project filtering](#per-project-filtering)
  - [Key → path extraction](#key--path-extraction)
  - [Replace handling](#replace-handling)
- [Lockfile format](#lockfile-format)
- [Performance characteristics](#performance-characteristics)
- [Known limitations](#known-limitations)
- [Upstreaming status](#upstreaming-status)

## Project overview

go2nix is a Go module → Nix lockfile generator + builder. It's a fork of
[gomod2nix] with one material change: the lockfile is keyed by
`module_path@version` instead of bare `module_path`.

[gomod2nix]: https://github.com/nix-community/gomod2nix

That single change has two downstream effects:

1. **Staleness becomes a build failure.** In upstream gomod2nix, the lockfile
   key is `module_path`; the Nix builder looks up the module by path, finds an
   entry, and vendors whatever version is recorded there. If you've bumped
   `go.mod` but not regenerated the lockfile, you silently vendor the old
   version. With composite keys, the builder filters by `path@version` from
   `go.mod` — a mismatched version simply isn't found, and Nix errors out.

2. **One lockfile for N projects.** Multiple versions of the same module can
   coexist in the lockfile. A monorepo with 10 Go projects can share one
   `go2nix.toml` at the root; each project's build filters it to just that
   project's requires. De-duplication is automatic.

The builder is a modified fork of gomod2nix's builder; the CLI is a from-scratch
rewrite (the upstream CLI has a different feature set and a different schema).

## Relationship to gomod2nix

Fork point: [`47d628dc`](https://github.com/nix-community/gomod2nix/commit/47d628dc3b506bd28632e47280c6b89d3496909d)
(Aug 2025). Since then, upstream added build hooks (`makeSetupHook`-based),
`mkGoCacheEnv` (build cache pre-warming), and `cachegen`; we have none of these
because we don't use them, not because they're incompatible.

**What we kept** from upstream:
- `buildGoApplication` / `mkGoEnv` / `mkVendorEnv` / `fetchGoModule` signatures
  and phases (we have the older inline-heredoc `buildPhase`, not the hooks)
- `parser.nix` (with one bugfix — see below)
- `symlink.go` / `install.go` / `fetch.sh` — unchanged or minimally modified

**What we added**:
- Composite-key filter in `mkVendorEnv` (~40 lines)
- `netrcFile` parameter for private module auth (same as upstream [PR #243])
- `goModFile` parameter to avoid IFD (same as upstream [PR #243])
- `parser.nix` fix for mixed single-line + parenthesized `require` blocks

**What we removed**:
- `hooks/`, `mkGoCacheEnv`, `cachegen/`, `updateScript`

[PR #243]: https://github.com/nix-community/gomod2nix/pull/243

## The core idea: composite keys

### Problem: silent staleness

gomod2nix's lockfile looks like this:

```toml
schema = 3
[mod]
  [mod."google.golang.org/grpc"]
    version = "v1.75.1"
    hash = "sha256-..."
```

`mkVendorEnv` reads every entry and fetches it. The project's `go.mod` isn't
consulted — whatever's in `gomod2nix.toml`, that's what gets vendored.

Now suppose a developer runs `go get google.golang.org/grpc@v1.76.0`. `go.mod`
and `go.sum` update. They forget to run `gomod2nix generate`. CI vendors
`grpc@v1.75.1`, the Go build succeeds (both are API-compatible), and the
production binary ships with an outdated gRPC. Every tool that reads `go.mod` —
`gopls`, `golangci-lint`, dependency scanners — sees `v1.76.0`. Only the Nix
build sees `v1.75.1`.

### Solution: key by `path@version`, filter by go.mod

```toml
[mod]
  [mod."google.golang.org/grpc@v1.75.1"]
    version = "v1.75.1"
    hash = "sha256-..."
  [mod."google.golang.org/grpc@v1.76.0"]
    version = "v1.76.0"
    hash = "sha256-..."
```

At eval time, `mkVendorEnv`:

1. Parses `go.mod`: `goMod.require = { "google.golang.org/grpc" = "v1.76.0"; ... }`
2. Builds a set of required keys: `{ "google.golang.org/grpc@v1.76.0" = true; ... }`
3. Filters `modulesStruct.mod` to only keys present in that set
4. **Checks** that every required non-local-replace module survived the filter;
   throws a clear error if not
5. Re-keys the result to bare module paths for `symlink.go`

Step 4 is how a stale lockfile or untidy `go.mod` becomes a **clear eval-time
error** instead of an opaque `go build` failure about missing packages:

```
error: go2nix lockfile is missing required module(s):
  google.golang.org/grpc@v1.76.0

Either go.mod is not tidy (require versions don't match MVS-resolved
versions), or the lockfile is stale. Run:
  go mod tidy
  <regenerate lockfile>
```

### The tidiness invariant

The filter reads versions from `go.mod`'s `require` directive. The CLI records
versions from `go mod download -json`, which are **MVS-resolved** — the
versions Go actually uses in the build list. These match **iff `go.mod` is
tidy**.

An untidy `go.mod` has `require` entries that are lower than what MVS picks at
build time (some transitive dependency requires a higher version). Example:

```
# go.mod (untidy)
require golang.org/x/mod v0.20.0 // indirect

# but golang.org/x/tools@v0.30.0 (a direct dep) requires x/mod v0.23.0
# so MVS picks v0.23.0, and `go mod download` records v0.23.0
```

After `go mod tidy`, the `require` directive is updated to `v0.23.0` and the
filter finds the lockfile entry.

#### Where this is enforced

**At generation time** (CLI): `collectModules` compares each
`go mod download`-resolved version against the `require` version and errors if
they differ. This is the primary check — you cannot generate an untidy
lockfile.

**At eval time** (Nix builder): if a required module has no entry in the
filtered lockfile, the builder throws a clear error naming the missing
`path@version`. This catches the case where `go.mod` was edited *after*
generation and the new version isn't in the lockfile.

**At build time** (`mvscheck` in `configurePhase`): after the vendor tree is
in place, [`mvscheck`](../go2nix/builder/mvscheck/mvscheck.go) constructs a
minimal GOMODCACHE from the vendored modules' `go.mod` files and runs
`go mod graph`. The trick:

> A tidy `go.mod`'s require block is **exactly** the set of MVS-selected
> versions (that's what `go mod tidy` writes). So a GOMODCACHE populated with
> `.mod` files for exactly those versions is **sufficient** for
> `go mod graph` to walk the full module graph — iff go.mod is tidy.
> If go.mod is untidy, the walk reaches a version not in the cache, and Go
> fails cleanly naming the missing `module@version`.

This catches the case the other checks miss: `go.mod` edited after generation
to an untidy version that *happens to exist* in the shared lockfile. Example:

- Project go.mod (edited, untidy): `require x/mod v0.20.0`
- Shared lockfile has `x/mod@v0.20.0` (project A uses it) AND `@v0.23.0`
  (this project when it was tidy)
- Filter picks `v0.20.0` (matches go.mod), eval check passes (entry found)
- `mvscheck` populates GOMODCACHE with `x/mod@v0.20.0.mod`, `x/tools@v0.30.0.mod`
- `go mod graph` reads `x/tools@v0.30.0.mod`, sees `require x/mod v0.23.0`,
  tries to look up `v0.23.0.mod`, fails: **"golang.org/x/mod@v0.23.0: module
  lookup disabled by GOPROXY=off"**

The check uses **Go's own MVS implementation** (`go mod graph`), not a
reimplementation. mvscheck does only: go.mod parsing, directory creation, and
exit-code interpretation. This matters because MVS has edge cases (module
graph pruning, `retract` directives, `+incompatible`, go-version selection)
that a hand-rolled checker would get wrong.

**Why this works offline**: the gomod2nix-style vendor tree preserves each
module's `go.mod` file (unlike `go mod vendor`, which strips them), and
`go mod graph` only needs `.mod` + `.info` files in GOMODCACHE — not sources
or zips.

`go mod tidy -diff` as a standalone CI check is still recommended (catches
tidiness before anything else), but `mvscheck` is the build-time backstop
that makes the shared lockfile safe against post-generation drift.

### Bonus: monorepo sharing

Because the filter is per-project, one `go2nix.toml` at the repo root can
contain the union of all modules across all projects, and each project's build
picks out exactly its subset.

```
monorepo/
  go2nix.toml              # 400 entries, union of all projects
  service-a/
    go.mod                  # requires 60 modules
    default.nix             # modules = ../go2nix.toml; filters to 60
  service-b/
    go.mod                  # requires 80 modules, 50 shared with service-a
    default.nix             # modules = ../go2nix.toml; filters to 80
```

## The Go CLI

[`go2nix/main.go`](../go2nix/main.go) — ~300 lines, 3 dependencies.

### Module collection

For each project directory:

1. Parse `go.mod` with `golang.org/x/mod/modfile` to find replace directives.
   Classify each as **local** (`replace foo => ../foo`, `New.Version == ""`) or
   **remote** (`replace foo => bar vX`, `New.Version != ""`).
2. Run `go mod download -json` in the project directory. This emits one JSON
   record per module, including the local cache directory where Go unpacked it.
3. For each record:
   - If the module is a local replace, skip it (go mod download skips these too,
     so we won't see them in practice, but the guard is explicit).
   - If it's a remote replace, the record's `Path` is the *replacement* path.
     Remap to the *original* path so the lockfile key matches the `require`
     directive.
   - Record `(origPath@version, fetchPath, localCacheDir)`.

The result across all projects is deduplicated by `origPath@version`.

### Caching

The existing `go2nix.toml` is read as a cache. A cache entry is reused only if
**both** the key matches **and** the recorded `Replaced` value matches the
current `fetchPath` — so a changed `replace` directive forces re-hash.

Entries not present in the current module set are dropped (the lockfile is the
exact union of currently-required modules, not an append-only log).

### NAR hashing

`nix hash path <dir>` on each module's local cache directory, in parallel
(`errgroup` bounded by `-j`, default `runtime.NumCPU()`).

`go mod download`'s cache directory layout is already Nix-friendly:
content-addressed, no non-reproducible metadata (the CLI strips `.DS_Store`
in [`fetch.sh`](../go2nix/builder/fetch.sh) at fetch time to match).

## The Nix builder

[`go2nix/builder/default.nix`](../go2nix/builder/default.nix). Mostly
gomod2nix's builder with the composite-key filter inserted.

### Per-project filtering

The filter runs inside `mkVendorEnv`, which is the derivation that produces
the vendor tree. See the Nix snippet above. The filtered set then flows through
the rest of gomod2nix's machinery unchanged: `fetchGoModule` for each entry,
`symlink.go` to build the vendor tree, `buildGoApplication` to run `go build`
with `-mod=vendor`.

### Key → path extraction

`symlink.go` (unchanged from upstream) expects a JSON map keyed by *bare module
path*. After filtering by composite key, we strip the version:

```nix
extractPath = key: removeSuffix "@${filteredMods.${key}.version}" key;
```

This is why `version` is redundant with the key suffix: it's cheaper to strip a
known suffix than to regex-parse the key in Nix.

The extraction is safe for all Go module path forms because Go module versions
always start with `v` and module paths never contain `@`:
- `github.com/foo/bar@v1.2.3` → `github.com/foo/bar`
- `github.com/foo/bar/v2@v2.1.0` → `github.com/foo/bar/v2` (vN-in-path convention)
- `github.com/foo/bar@v0.0.0-20240101000000-abcdef` → `github.com/foo/bar` (pseudo-version)

### Replace handling

**Local replaces** (`replace foo => ../foo`): not in the lockfile at all. The
builder's `localReplaceCommands` creates symlinks directly into the source tree.
The filter naturally excludes them because `go.mod`'s `require` for a local
replace points to a pseudo-version like `v0.0.0-00010101000000-000000000000`
that won't exist in the lockfile.

**Remote replaces** (`replace foo => bar vX`): the lockfile entry is keyed by
the *original* path and the *replacement's* version (`foo@vX`) with
`replaced = "bar"`. The filter computes the effective version by consulting
`goMod.replace`:

```nix
effectiveVersion = path:
  let repl = goMod.replace.${path} or null;
  in if repl != null && repl ? version  # remote replace (local replaces have .path, not .version)
     then repl.version
     else goMod.require.${path};
```

`fetchGoModule` then fetches `bar@vX` instead of `foo@vX` via `meta.replaced`.

## Lockfile format

Plain TOML, no schema version marker (the key format is self-identifying:
upstream keys don't contain `@`):

```toml
# go2nix lockfile: module@version -> NAR hash.

[mod]
  [mod."github.com/BurntSushi/toml@v1.5.0"]
    version = "v1.5.0"
    hash = "sha256-wX8bEVo7swuuAlm0awTIiV1KNCAXnm7Epzwl+wzyqhw="
  [mod."github.com/original/pkg@v2.0.0"]
    version = "v2.0.0"
    hash = "sha256-..."
    replaced = "github.com/fork/pkg"
```

Keys are sorted (BurntSushi/toml sorts map keys). Output is byte-deterministic:
running the generator twice produces identical bytes. This matters for merge
conflicts — concurrent edits to different entries produce clean diffs.

## Performance characteristics

**Generator**: dominated by `go mod download` (one invocation per project,
serial) and `nix hash path` (parallel). Hashing ~300 modules with a warm
`GOMODCACHE` takes ~5s on a homespace VM with `-j 64`.

**Nix eval**: this is the benchmark dimension this repo cares about. The filter
is `O(R * M)` where R = `require` entries, M = lockfile entries (`filterAttrs`
walks M, each `hasAttr` on the R-sized set is O(1)). For the torture test
(~1200 modules), expected to be marginally slower than gomod2nix's unfiltered
`mapAttrs` since we do more work per entry — but we're filtering from a
*single-project* lockfile here, so R ≈ M and the filter keeps everything. The
monorepo scenario is where it pays off: M = 400 (union), R = 60 (one project),
and gomod2nix would need a 60-entry per-project lockfile anyway.

**Nix build**: identical to gomod2nix — same vendored `go build`.

## Known limitations

- **Version-qualified replaces** (`replace foo v1.0.0 => bar v2.0.0`): the
  go.mod parser keys this by `"foo v1.0.0"` (path + old-version), but the
  filter's `effectiveVersion` only looks up by bare path. Fixable by also
  probing `goMod.replace."${path} ${requireVersion}"`. Uncommon in practice.

- **No `go.work` support**: the CLI takes project directories, not a workspace
  file. Workspaces with `replace` directives across workspace modules would
  need additional handling.

- **Serial `go mod download`**: per project directory. Trivially parallelizable
  if needed.

- **Builder is pre-hooks**: forked before upstream's `makeSetupHook` refactor.
  Using the upstream `goBuildHook`/`goCheckHook` would allow cleaner
  `buildPhase`/`checkPhase` overrides.

## Upstreaming status

Four independent changes vs upstream; two are already upstream PRs:

| Change | Status |
|---|---|
| `netrcFile` + `goModFile` | [PR #243](https://github.com/nix-community/gomod2nix/pull/243) open since Dec 2025, awaiting review |
| `parser.nix` mixed-require fix | Not yet submitted; trivial standalone bugfix |
| **Composite keys** | Not yet submitted; schema-breaking, needs issue-first discussion |
| Shared-lockfile CLI | Depends on composite keys |

Relevant upstream issues composite keys address:
- [#119](https://github.com/nix-community/gomod2nix/issues/119) — check for changed replace path
- [#169](https://github.com/nix-community/gomod2nix/issues/169) — subcommand to detect stale lockfile
- [#108](https://github.com/nix-community/gomod2nix/issues/108) — avoid unrelated deps with go.work
