**What:** `test-package-nwg-drawer-dynamic` is wired (#108) but the
synthesised link drv's `NIX_LDFLAGS` propagation may have edge cases
with very deep propagated-input chains.

**Why:** Only one GTK-shaped test case; the salted `NIX_LDFLAGS_<suffix>`
approach should be robust but isn't differentially tested.

**Approach:**
- Add a second dynamic-mode pkg-config fixture with a different
  propagated-dep shape (e.g. `tests/packages/vinegar/dynamic.nix` —
  vinegar uses GTK4)
- Compare the resolved LibDirs against `pkg-config --libs` output

**Blockers:** none
