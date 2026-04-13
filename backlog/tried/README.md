# backlog/tried/

Abandoned experiments. Each file records what was attempted, the gate
result (% regression or correctness diff), and why it was dropped — so
the same approach isn't blindly retried.

Seed entries from the perf-research and audit work that's already
shipped or ruled out:

- `perf-tier1-hoists.md` — shipped as #105 (−14% primops)
- `perf-tier2-filter-precompute.md` — shipped as #107
- `perf-tier3-plugin-offload.md` — shipped as #109
- `perf-nestedmodule-readdir-walk.md` — prototyped during #107, dropped:
  Nix-side `readDir` per directory adds more primops than it saves;
  builtins.path's C++ traversal already enumerates them. Correct fix
  was Tier-3 (plugin emits boundaries).
