**What:** PR #48 (`feat/resolve-dag-cache`) — persistent on-disk cache
for `resolveGoPackages` keyed on toolchain identity + go.sum hash. On
hold per the user.

**Why:** This is the remaining structural eval-perf lever after Tier 1-3.
Repeated instantiates of an unchanged module skip the `go list` invocation
entirely.

**Approach:** when revived:
- Rebase onto current main (#109+ changed the output schema; cache key
  in `a51be38` already includes `GOFIPS140`)
- The cache must include the schema version so old cache entries are
  invalidated when output fields are added
- Thread `GO2NIX_RESOLVE_CACHE_DIR` from the monorepo's coo wrapper

**Blockers:** user explicitly said #48 won't be merged soon (2026-04-10).
DO NOT pick this without checking with them first. File under needs-human
if triage selects it.
