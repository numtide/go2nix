**What:** `pkg/compile/cgo.go` may still write `.has_cgo` / `.has_cxx`
filesystem markers that nothing reads after #85 (which moved cxx
detection to a closure-computed `sp.CXX` in linkbinary).

**Why:** Dead writes; if confirmed, removing them is net-negative-line
and slightly speeds the cgo compile path.

**Approach:**
- `rg "\.has_cgo|\.has_cxx" go/go2nix/` — find writers and readers
- If only writers remain: delete the writes, golden + canary regen
- If a reader exists: document why and close as not-dead

**Blockers:** none
