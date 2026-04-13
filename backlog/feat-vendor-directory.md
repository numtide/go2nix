**What:** `vendor/` directory mode unsupported. With `-mod=vendor`,
cmd/go reads deps from `vendor/` instead of GOMODCACHE — go2nix's
resolver always uses go.sum + module cache.

**Why:** Some upstream projects ship vendored. Low priority for the
monorepo (no service vendors), but a real cmd/go feature gap.

**Approach:**
- `resolveGoPackages` (rust): detect `vendor/modules.txt`, skip
  go.sum hash resolution, emit `vendored: true`
- `nix/dag`: if vendored, third-party packages come from
  `src + "/vendor/<path>"` instead of `fetchGoModule`
- Fixture: `tests/fixtures/vendored/` with a checked-in `vendor/`

**Blockers:** none, but it's a structural feature — likely 2-3 rounds
