# Recursive-Nix Internals and Scaling Analysis

Analysis of the Nix source code (`src/libstore/`) to understand how recursive-nix
works and what constraints it imposes on large dependency graphs.

## How the recursive-nix sandbox works

When a derivation declares `requiredSystemFeatures = ["recursive-nix"]`, Nix runs
a restricted daemon inside the build sandbox.

### Daemon architecture

`src/libstore/unix/build/derivation-builder.cc:1148-1210`:

1. **Socket**: A Unix domain socket `.nix-socket` is created in `$TMPDIR`.
   The builder sees it via `NIX_REMOTE=unix://<sandbox-tmpdir>/.nix-socket`.

2. **Accept loop**: A single daemon thread accepts connections. Each connection
   spawns a **new worker thread** — there is no connection limit or pool:

   ```cpp
   auto workerThread = std::thread([store, remote{std::move(remote)}]() {
       daemon::processConnection(
           store, FdSource(remote.get()), FdSink(remote.get()),
           NotTrusted, daemon::Recursive);
   });
   daemonWorkerThreads.push_back(std::move(workerThread));
   ```

3. **Cleanup**: On builder exit, `stopDaemon()` joins all worker threads
   **sequentially** (line 1234-1237). Two FIXMEs in the source:
   ```cpp
   // FIXME: should prune worker threads more quickly.
   // FIXME: shutdown the client socket to speed up worker termination.
   ```

### RestrictedStore

All operations go through a `RestrictedStore` wrapper
(`src/libstore/restricted-store.cc`, `include/nix/store/restricted-store.hh`).

**Path whitelisting**: Only paths in the original input closure or paths added
via recursive Nix calls are accessible. `RestrictionContext` tracks:
- `originalPaths()` — derivation inputs from eval time
- `addedPaths` — paths added during the build via recursive Nix calls
- `addedDrvOutputs` — realisations added during the build

**No caching**: The restricted store config disables the path info cache
(`pathInfoCacheSize = 0`, line 1155) and sets bogus state/log directories.
Every store query goes through the socket to the real local store.

### The critical `buildPathsWithResults` path

`restricted-store.cc:263-298` — this runs for every `nix build` call inside
the sandbox:

```cpp
std::vector<KeyedBuildResult> RestrictedStore::buildPathsWithResults(...) {
    // 1. Check all requested paths are allowed
    for (auto & req : paths) {
        if (!goal.isAllowed(req))
            throw InvalidPath("cannot build '%s' in recursive Nix ...", ...);
    }

    // 2. Delegate to the real store
    auto results = next->buildPathsWithResults(paths, buildMode);

    // 3. Compute closure of ALL new output paths
    StorePathSet closure;
    next->computeFSClosure(newPaths, closure);    // <-- O(n) walk
    for (auto & path : closure)
        goal.addDependency(path);                 // <-- grows the whitelist

    return results;
}
```

**The `computeFSClosure` call is the scaling bottleneck.** It walks the entire
reference graph of newly-built outputs. If you build packages one at a time in
topological order, later packages have closures containing all earlier outputs.
This gives **O(n^2) total closure walking** across the full resolve.

## Impact on go2nix

### Why our architecture avoids the bottleneck

The go2nix resolve flow runs inside the recursive-nix wrapper but is designed
to minimize `nix build` calls:

| Step | Operation | Nix calls | Hits `computeFSClosure`? |
|------|-----------|-----------|--------------------------|
| Create module FODs | `nix derivation add` x N | N cheap JSON→hash ops | No |
| Materialize FODs | **1** batched `nix build` | 1 call, N installables | **Once** |
| Create package CAs | `nix derivation add` x N | N cheap JSON→hash ops | No |
| Create link/collector | `nix derivation add` x 1 | 1 cheap JSON→hash op | No |
| Copy `.drv` to `$out` | `copyFile` | 0 Nix calls | No |

The only `nix build` inside the wrapper is Step 2 (FOD materialization), issued
as a **single batched call**. This triggers `computeFSClosure` once for the
union of all FOD outputs — acceptable even for 200+ modules since FODs have
small closures (just their own output, no inter-FOD references).

Package compilation happens **outside** the wrapper entirely. The wrapper
outputs a `.drv` file; `builtins.outputOf` causes Nix's external scheduler to
build the full package DAG with native parallelism (`--max-jobs`), no
restricted store overhead, and full path info caching.

### `nix derivation add` cost inside the wrapper

Each `nix derivation add` call:

1. Spawns a `nix` CLI process (~20-26ms startup overhead alone)
2. Opens a socket connection → spawns a worker thread on the daemon
3. Parses JSON, computes derivation hash, writes `.drv` to store (~6-12ms)
4. Worker thread stays alive until the connection closes

**Benchmarked on 32-core x86_64, Nix 2.31.3, SSD:**

| Derivation size | JSON | Per call (sequential) |
|-----------------|------|-----------------------|
| Minimal (no deps) | 394 B | 32.3ms |
| Realistic (5 deps, 50 importcfg entries) | 6 KB | 33.2ms |
| Large (20 deps, 200 importcfg entries) | 26 KB | 35.0ms |

JSON size has minimal impact (~3ms difference for 65x larger payload).
~80% of the per-call cost is Nix CLI process startup (measured: `nix store
info` alone takes 26ms, `nix eval 1+1` takes 21ms).

**No degradation over time**: 7 sequential batches of 500 adds each showed
consistent ~32ms/call from batch 1 through batch 7 (3,500 total).

**Parallelism saturates at P=4** (measured with 500 derivations):

| Parallelism | Per call (effective) | Speedup |
|-------------|---------------------|---------|
| P=1 | 33.0ms | 1.0x |
| P=4 | 10.2ms | 3.3x |
| P=8 | 10.1ms | 3.3x |
| P=16 | 10.1ms | 3.3x |
| P=32 | 10.1ms | 3.3x |

The plateau at P=4 suggests the bottleneck is SQLite write serialization
in the Nix store database (`/nix/var/nix/db/db.sqlite`). Multiple concurrent
`nix derivation add` processes contend on the single SQLite writer lock.

### FOD materialization cost

The single batched `nix build` for FODs triggers:
- Network fetches (bounded by `max-substitution-jobs`, default 16)
- One `computeFSClosure` call for the union of all FOD outputs
- FOD outputs are independent (no cross-references), so closure is O(N) total paths

For 478 modules (app-full scale), cold fetch is I/O-bound (minutes on first
build). On a warm cache (FODs already in store from a previous build), this
step is nearly instant.

### `go list` cost

Benchmarked on app-full (3,527 packages including 262 stdlib):

| Metric | Value |
|--------|-------|
| Wall time (cold) | 2.2s |
| Wall time (warm) | 1.0s |
| RSS memory | 201 MB |
| JSON output | 30 MB |
| CPU time (user+sys) | 6.3s (uses multiple cores) |

`go list` is fast and memory-efficient at this scale. For a 6M-line monorepo
with 10,000+ packages, extrapolating linearly: ~3-6s wall time, ~600MB RSS,
~90MB JSON. This is well within sandbox limits.

### What would be problematic (but we avoid)

If we built packages **inside** the wrapper (like nix-ninja does for C/C++),
each `nix build` would trigger `computeFSClosure` on increasingly large
closures:

- Package 1: closure size 1
- Package 2: closure size ~2
- Package N: closure size ~N
- Total: O(N^2) path lookups

For 3,250 packages this would be ~5.3 million path lookups through the
uncached restricted store socket. nix-ninja accepts this cost because C/C++
requires building inside the wrapper (header dependencies discovered at compile
time via `-MD`). Go's import graph is fully known after `go list`, so we avoid
this entirely.

## Other Nix internals relevant to dynamic derivations

### `nix derivation add` internals

`src/nix/derivation-add.cc:33-43`:

```cpp
auto json = nlohmann::json::parse(drainFD(STDIN_FILENO));
auto drv = Derivation::parseJsonAndValidate(*store, json);
auto drvPath = store->writeDerivation(drv, NoRepair);
```

Three steps: parse JSON, validate + compute output paths, write to store.
`writeDerivation` (in `derivations.cc:135-160`) hashes the ATerm format and
calls `addToStoreFromDump()`. A temporary GC root is created to prevent
collection during the build.

### Dynamic derivations feature

`src/libutil/experimental-features.cc:239-250`:

Enables two things:
1. **Text-hashed derivation outputs** (`outputHashMode = "text"`) — so the
   wrapper can output a `.drv` file
2. **Nested derivation references** — `builtins.outputOf` can reference outputs
   of derivations that are themselves outputs of other derivations

### `builtins.outputOf` resolution

`src/libexpr/primops.cc:2444-2488`:

Creates a `SingleDerivedPath::Built` value. For our wrapper, this forms a
double-nested chain:

```
Built {
  drvPath: Built { drvPath: Opaque{wrapper.drv}, output: "out" },
  output: "out"
}
```

At build time, Nix resolves this by:
1. Building the wrapper → getting the `.drv` file
2. Reading the `.drv` from the wrapper's text-mode output
3. Building that `.drv` → transitively building all package CAs

### Resolution termination

`docs/dynamic-and-ca-derivations.md:131-133`:

> For dynamic derivation graphs, resolution is **not** guaranteed to terminate.
> Dynamic derivations (filled in by previous resolution) may have more transitive
> dependencies than the original derivation, potentially leading to infinite
> resolution chains.

This is a theoretical concern for arbitrary dynamic derivation chains. It does
**not** affect go2nix because our wrapper produces a fixed `.drv` file (no
further dynamic nesting). The chain is always exactly 2 levels deep:
wrapper.drv → package-graph.drv → binary output.

### Downstream placeholders

`src/libstore/downstream-placeholder.cc`:

Three placeholder types:
- **Standard**: `SHA256("nix-output:<name>")` → for non-CA outputs
- **CA (unknownCaOutput)**: `SHA256("nix-upstream-output:<hash>:<name>")` → for
  CA derivation outputs (what we use for package `.a` files)
- **Dynamic (unknownDerivation)**: nested hash compression → for outputs of
  dynamic derivations (not needed by go2nix since we don't nest further)

## Scaling at monorepo scale

Reference project: `app-full` from go2nix-torture — 478 modules, 3,265
non-stdlib packages (3,527 total including 262 stdlib), 14 local workspace
modules with replace directives, 1 MB lockfile. The real target codebase is
6M+ lines of Go with even more modules.

### Benchmarked cost breakdown inside the wrapper

All measurements on 32-core x86_64, Nix 2.31.3, SSD storage.

| Step | Count (app-full) | Measured time | Source |
|------|-------------------|---------------|--------|
| `nix derivation add` (FODs) | 478 | **15.3s** sequential | 478 x 32ms/call |
| `nix build` (FODs, batched) | 1 call, 478 installables | Warm: <1s, Cold: minutes | I/O bound |
| `go list -json -deps` | 1 call, 3,527 pkgs | **1.0-2.2s** | Benchmarked on app-full |
| JSON parse (30 MB) | — | **~0.3s** | Benchmarked with jq |
| `nix derivation add` (packages) | 3,265 | **105s** sequential | 3,250 x 32ms, confirmed at scale |
| `nix derivation add` (link) | 1-2 | **<0.1s** | Negligible |
| **Total wrapper (sequential, warm)** | **3,745 adds** | **~122s** | Dominated by drv adds |
| **Total wrapper (P=4, warm)** | **3,745 adds** | **~40s** | 3.3x speedup measured |

The `nix derivation add` cost was confirmed at full scale: 3,250 sequential
calls took **105s** (32.3ms/call), and 3,250 calls at P=4 took **39s**
(12.0ms effective/call).

### Where the 32ms per call goes

| Component | Time | % |
|-----------|------|---|
| Nix CLI process startup | ~21-26ms | ~75% |
| JSON parse + derivation hash | ~3-5ms | ~12% |
| SQLite store write | ~3-5ms | ~13% |

Measured by comparing `nix eval 1+1` (21ms, pure startup) and `nix store info`
(26ms, startup + store connection) against the full `nix derivation add` (32ms).

### `go list` at scale

Benchmarked on app-full (3,527 packages):

| Metric | Value |
|--------|-------|
| Wall time (warm) | 1.0s |
| Wall time (cold) | 2.2s |
| RSS memory | 201 MB |
| JSON output | 30 MB |
| CPU time (user+sys) | 6.3s across multiple cores |

Extrapolation for 10,000 packages (linear scaling): ~3s wall, ~600MB RSS.
The Nix sandbox does not impose memory limits beyond system RAM. `go list` at
this scale is **not** a bottleneck.

### Nix store pressure

3,745 `.drv` files per build, each 1-5KB = ~5-20MB total store pressure —
negligible.

The SQLite database gets one write per `.drv`. Benchmarked: no degradation from
batch 1 (adds 1-500) through batch 7 (adds 3,001-3,500), all at ~32ms/call.
However, this was on SSD. On network-attached storage (common in CI), SQLite
write latency would increase the per-call cost.

### External build graph (after wrapper)

Once the wrapper produces the `.drv` file, Nix builds the full package DAG
externally. For 3,265 CA derivations:

- Nix's scheduler handles parallelism via `--max-jobs`
- Each package derivation is small (compile one package → one `.a` file)
- CA deduplication means unchanged packages are instant cache hits
- The link step depends on all transitive packages completing first

On incremental rebuilds (the common case), most packages are CA cache hits and
only changed packages + their reverse dependencies recompile.

### Potential optimizations

**1. Parallel `nix derivation add` per topological level**

Currently, package derivations are added in serial topological order because
each package needs its dependencies' `.drv` paths for placeholder computation.
However, packages at the same topological level are independent:

```
Level 0 (leaves):     add N₀ packages in parallel
Level 1:              add N₁ packages in parallel (needs level 0 drv paths)
Level 2:              add N₂ packages in parallel
...
```

Measured speedup at P=4: 3.3x (saturates due to SQLite write lock).
This reduces app-full wrapper time from **~122s to ~40s**.

Further parallelism beyond P=4 provides no benefit — the bottleneck shifts
from process startup to SQLite write contention.

**2. Nix daemon socket protocol (bypass CLI)**

The `nix` CLI is 75% of per-call cost. Inside the recursive-nix sandbox,
there is already a Unix socket at `$NIX_REMOTE`. We could speak the Nix
daemon protocol directly from Go (or via a helper binary), eliminating
per-call process spawn entirely.

Estimated per-call cost without CLI: ~6-10ms (just protocol + store write).
At 3,745 calls: **~22-37s sequential, ~8-12s at P=4** vs current 40-122s.

This is how nix-ninja works: it calls libstore via FFI from Rust, avoiding
CLI overhead. A Go equivalent would use the daemon socket protocol directly.

**3. Batch derivation add protocol**

The Nix daemon protocol (`wopAddDerivation`) handles one derivation per
request. A hypothetical batch operation sending multiple derivations in a
single socket session would amortize connection overhead and potentially
allow the daemon to batch SQLite writes. This would require Nix upstream
changes.

**4. Pre-filter packages**

For monorepos where only a subset of packages are needed (e.g.,
`--sub-packages ./cmd/server`), `go list` already filters the transitive
closure. But if the user requests `./...`, the full graph is included. We
could explore filtering out test-only packages or packages not reachable from
main.

**5. Incremental wrapper output**

The wrapper derivation is text-mode CA. If the lockfile `[mod]` section hasn't
changed and the source tree is identical, the wrapper's `.drv` output is a
cache hit — the entire resolve flow is skipped. This is the expected fast
path for incremental development.

### Extrapolation to larger monorepos

| Scale | Packages | Sequential | P=4 | Direct socket (est.) |
|-------|----------|------------|-----|---------------------|
| app-full | 3,265 | 105s | 39s | ~20-33s |
| 2x app-full | ~6,500 | 210s | 78s | ~39-65s |
| 10,000 pkgs | 10,000 | 320s (5.3m) | 120s (2.0m) | ~60-100s |

All times are for `nix derivation add` only (the dominant cost). Add ~2s for
`go list` and ~0.3s for JSON parsing at each scale. FOD build is a one-time
cost on cold cache.

## Benchmark environment

```
Machine:  32 cores, x86_64
Nix:      2.31.3
Go:       1.25.7
Storage:  SSD
Project:  app-full (478 modules, 3,265 non-stdlib packages)
```

## Summary

| Concern | Status | Measured |
|---------|--------|---------|
| O(n^2) closure walks | **Avoided** | Only 1 `nix build` in wrapper (FODs), packages built externally |
| `nix derivation add` latency | **Dominant cost** | 32ms/call sequential; 75% is CLI process startup |
| Parallelism ceiling | **P=4 saturation** | 3.3x speedup, then SQLite write lock bottleneck |
| Full app-full wrapper (sequential) | **~122s** | 3,745 adds x 32ms + 2s go list |
| Full app-full wrapper (P=4) | **~40s** | 3,745 adds x 12ms effective + 2s go list |
| Degradation over time | **None** | 32ms/call consistent across 3,500 adds |
| `go list` | **Fast** | 1.0s warm, 201 MB RSS for 3,527 packages |
| FOD fetch (warm cache) | **<1s** | Already in store |
| `go list` memory at 10K pkgs | **~600 MB** | Linear extrapolation from 201 MB at 3,527 |

The architecture avoids the O(n^2) `computeFSClosure` bottleneck. The dominant
cost is `nix derivation add` CLI process startup (75% of per-call time).
Parallelizing to P=4 reduces app-full wrapper time from ~122s to ~40s.
Bypassing the CLI entirely (direct daemon socket protocol) could bring it to
~20-30s. All costs scale linearly with package count.
