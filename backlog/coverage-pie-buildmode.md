**What:** `buildmode=pie` is plumbed (linkbinary handles `-buildmode=pie`
and adds `-shared` to gcflags) but no fixture exercises it end-to-end.

**Why:** PIE is the default on some distros; one untested branch in
linkbinary.go.

**Approach:**
- New `tests/fixtures/pie-basic/` (minimal main, dag.nix sets
  `buildMode = "pie"`)
- Wrapper asserts `file` output shows `pie executable` and modinfo
  has `build -buildmode=pie`
- Add to `test-golden-vs-gobuild` (vanilla side: `go build -buildmode=pie`)

**Blockers:** none
