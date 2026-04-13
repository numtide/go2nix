**What:** `packages/go2nix/default.nix:13` uses `cleanSource ../../go/go2nix`
which includes `*_test.go`. Any test-only PR (e.g. #103) changes the CLI
drv hash → cascades to every fixture → `test-drv-canary` requires
regeneration even though nothing functional changed.

**Why:** Noise in the canary signal; test-only PRs shouldn't need
`expected.txt` regen.

**Approach:**
- Replace `cleanSource` with `lib.fileset.toSource` filtering out
  `**/*_test.go` and `**/testdata/**`
- Prove canary is THEN stable across a synthetic test-only diff
- One-time canary regen for the filter change itself

**Blockers:** none (but changes the CLI drv hash once, so batch with
other CLI-touching items if possible)
