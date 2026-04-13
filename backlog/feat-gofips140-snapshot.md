**What:** `GOFIPS140=v1.0.0` (snapshot mode) compiles the stdlib snapshot
correctly (via #34's goEnv plumbing) but linking fails: stdlib.nix's
importcfg generator doesn't list snapshot-remapped paths under
`crypto/internal/fips140/v*/`.

**Why:** Completes FIPS support beyond `=latest` (#100). Low priority —
no known consumer needs certified-module builds.

**Approach:**
- `nix/stdlib.nix`: when `goEnv.GOFIPS140` is a version, mirror
  `cmd/go/internal/fips140.ResolveImport` so the importcfg includes
  the remapped paths
- Extend `tests/fixtures/fips140-latest/` with a snapshot variant
  (gated on `ls $GOROOT/lib/fips140/*.zip` having a usable version)

**Blockers:** needs a Go release with a published lib/fips140 snapshot
zip; verify what 1.26.x ships
