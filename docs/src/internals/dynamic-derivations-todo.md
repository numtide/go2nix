# Dynamic Derivations — Remaining Work

All code is implemented. What remains is integration testing that requires
a Nix daemon with `recursive-nix`, `ca-derivations`, and `dynamic-derivations`
experimental features enabled.

## Integration testing

- `go2nix resolve` producing a `.drv` file for a real Go project
- `nix build` of a project using `buildGoApplicationDynamic`
- `go2nix generate --mode=dynamic` producing a lockfile that `resolve` can consume
- Fallback to `buildGoApplicationLockfile` when `builtins.outputOf` is unavailable
- CA deduplication: editing a comment should not trigger recompilation of unchanged packages
- Multi-binary projects (multiple main packages → collector derivation)
- Cgo projects with `packageOverrides`
- Projects with local sub-packages in subdirectories
- Projects with local replace directives (e.g., `replace foo => ./libs/foo`)
- Private modules using `netrcFile`

## Known limitations

- Large dependency graphs: see [recursive-nix-internals.md](recursive-nix-internals.md) for a full analysis with benchmarks. The architecture avoids the O(n^2) closure walk bottleneck. The dominant cost is `nix derivation add` at 32ms/call (75% CLI startup). For app-full (3,265 packages): **~122s sequential, ~40s at P=4**. Parallelism saturates at P=4 due to SQLite write lock. Bypassing the CLI (direct daemon protocol) could reach ~20-30s. Scales linearly with package count.
- Replace directives pointing to paths outside the source root: local replaces within `src` are handled correctly via `pkg.Dir` from `go list`, but replaces pointing outside `src` (e.g., `replace foo => ../../other-repo`) require the user to include the target in their Nix `src` attribute
