**What:** `pkg/lockfile/` may have unused code paths after #80/#81
(which made default-mode lockfile-free) and #106 (which fixed dynamic
mode's GOMODCACHE setup to derive everything from go.sum directly).

**Why:** ~several hundred LOC potentially orphaned; clarity + reduced
maintenance surface.

**Approach:**
- `rg "lockfile\." go/go2nix/ --type go -g '!*_test.go'` — find consumers
- For each exported func/type: trace callers; delete if none outside
  the package (or only from tests)
- `go build ./...` + golden + canary regen

**Blockers:** none
