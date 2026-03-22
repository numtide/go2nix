# DAG Test Support Phase 2 Plan

## Goal

Restore correct DAG-mode test behavior beyond Phase 1. This phase must cover:

1. Test-only third-party dependencies.
2. External tests that require recompilation of local dependents after the
   package-under-test archive is replaced.

Phase 1 behavior must keep working. Normal builds must not regress.

## Ground Truth

Use these as references while implementing:

- Go semantics:
  - `cmd/go/internal/load/test.go`
- Bazel direct-compile model:
  - `go/private/rules/test.bzl`
  - `go/tools/builders/generate_test_main.go`
- Current go2nix code:
  - `go/go2nix/pkg/testrunner/testrunner.go`
  - `go/go2nix/cmd/go2nix/testpkgs.go`
  - `nix/dag/default.nix`
  - `nix/dag/hooks/link-go-binary.sh`
  - `../go-nix-plugin/cpp/resolve_go_packages.cc`

## Non-goals

- Coverage parity.
- Race, msan, or asan-specific test semantics.
- Large unrelated refactors.
- Perfect monorepo ergonomics if that complicates core correctness.

## Definition of Done

After this phase:

- `doCheck = true` works for:
  - internal tests
  - external tests
  - test-only third-party imports like `testify`
  - local xtest dependency graphs that need recompilation
- non-check builds still avoid test-related fan-in and avoid new direct
  dependencies on all test package outputs
- unsupported cases fail explicitly instead of silently miscompiling

## Workstream 1: Test Dependency Discovery in the Plugin

File:

- `../go-nix-plugin/cpp/resolve_go_packages.cc`

Problem:

- The current DAG graph comes from `go list -deps` on build packages.
- Test-only third-party imports never enter `packages` or `depsImportcfg`.

Plan:

1. Extend `builtins.resolveGoPackages` to gather test dependency information.
2. Keep the build graph and test graph separate.
3. Return additive test-oriented attrs instead of widening the semantics of the
   existing build attrs.

Recommended shape:

- `testThirdPartyPackages`: attrset keyed by import path, matching the existing
  `packages` schema as closely as possible.
- Optional: `testRoots` or `testDepsByLocalPackage` if the Nix side or the test
  runner needs package-level test dependency roots.

Implementation notes:

- Prefer an additional `go list` pass for tests over broadening the build graph.
  This keeps the build graph semantics clean and is the recommended Phase 2
  approach even if it adds a second eval-time `go list` invocation.
- Prefer targeting only local packages that actually have `TestGoFiles` or
  `XTestGoFiles` from the first pass, instead of a blanket `./...` style test
  query. This avoids pulling test-only dependencies for packages that will
  never be exercised by `doCheck`.
- Include imports from both same-package tests and xtests.
- Preserve current behavior when `doCheck = false`.

Acceptance:

- A package with `_test.go` importing `github.com/stretchr/testify/require`
  produces plugin output that includes that dependency even if production code
  does not import it.

## Workstream 2: Build Test-Only Third-Party Archives in Nix

File:

- `nix/dag/default.nix`

Problem:

- Once test-only third-party packages are discovered, they must be compiled for
  the check phase.

Plan:

1. Add a `testPackages` derivation set, parallel to `packages`.
2. Reuse the `goModuleHook` compilation pipeline.
3. Instantiate `testPackages` only when `doCheck = true`.

Instantiation order matters:

1. Build normal third-party package derivations (`packages`).
2. Build test-only third-party package derivations (`testPackages`) using the
   normal build importcfg as input.
3. Build `testDepsImportcfg` bundling:
   - normal third-party packages
   - local packages
   - test-only third-party packages
   - stdlib importcfg

Recommended importcfg strategy:

- Keep `depsImportcfg` for normal builds.
- Add `testDepsImportcfg` for checks.
- `testDepsImportcfg` should include:
  - stdlib importcfg
  - normal third-party package archives
  - local package archives
  - test-only third-party package archives

Do not:

- Mutate `depsImportcfg` unconditionally.
- Pull test-only packages into normal build inputs.

Alternative implementation:

- Instead of a separate `testDepsImportcfg` derivation, the hook may append
  test-only package entries to `$NIX_BUILD_TOP/importcfg` during `checkPhase`.
- In that model, Nix would expose test-only archive paths as a structured attr
  such as `goTestArchives`, conditional on `doCheck`.
- This trades a slightly more complex hook for fewer derivations and may be a
  good fit if the project prefers the existing Phase 1 pattern over another
  importcfg bundle derivation.

Acceptance:

- With `doCheck = false`, the build graph stays close to current behavior.
- With `doCheck = true`, test-only third-party archives are available to the
  check phase through a test-specific importcfg.

## Workstream 3: Extend the Hook Contract for Tests

Files:

- `nix/dag/default.nix`
- `nix/dag/hooks/link-go-binary.sh`

Goal:

- Provide the check phase with:
  - local package archives
  - test importcfg
  - optionally a test-specific source root

Plan:

1. Keep Phase 1 `goLocalArchives` behavior.
2. Add a check-only attr or env scalar for the test importcfg path, for example
   `goTestImportCfg`.
3. If needed later, add `goTestModuleRoot` or `goTestSrcRoot`.

Hook behavior:

- Reconstruct the local archive tree as in Phase 1.
- Invoke `test-packages` with the test importcfg once Workstream 2 lands.

Design rule:

- The hook should orchestrate inputs, not reimplement graph logic.

## Workstream 4: Minimal CLI Changes

File:

- `go/go2nix/cmd/go2nix/testpkgs.go`

Goal:

- Extend the CLI only as needed for new test inputs.

Recommended changes:

- Keep `--local-dir`.
- Keep existing flags unless removal is clearly justified.
- Add `--test-import-cfg` only if using a separate importcfg file is clearer
  than reusing `--import-cfg`.
- Add `--src-root` only if test source roots differ from the module root.

Rule:

- Keep the CLI backward-compatible if possible.

## Workstream 5: Rework the Test Runner for Graph Substitution

File:

- `go/go2nix/pkg/testrunner/testrunner.go`

This is the core of Phase 2.

Current runner already does:

- discover local packages
- build internal archive
- build external archive
- generate testmain
- compile testmain
- link and run

What must change:

- When the internal archive replaces the original package archive, local
  dependents in the xtest graph may need recompilation.
- This is the `go2nix` equivalent of Go's `recompileForTest` and Bazel's
  `_recompile_external_deps`.

Implementation plan:

1. Build local package metadata graph.
   - Use `localpkgs.ListLocalPackages`.
   - Build:
     - `import path -> package metadata`
     - `import path -> direct local imports`
   - Resolve archive paths from importcfg or the existing archive layout.

2. For each package under test:
   - compile internal archive
   - create a test-local importcfg copy
   - override the package-under-test entry to point to the internal archive

3. If there is no xtest:
   - keep the current flow
   - compile testmain and run

4. If xtest exists:
   - compute affected local packages that must be recompiled
   - starting from xtest's local dependency graph, identify all local packages
     that transitively depend on the package under test
   - topo sort that slice

5. Recompile affected local packages in topo order.
   - each recompilation uses the overridden test importcfg
   - after compiling each archive, update the test importcfg to point to the
     newly built archive
   - downstream recompiles must see the test copy, not the original

6. Compile the xtest archive against the updated test importcfg.

7. Generate and compile testmain.

8. Link and run.

Important semantic points:

- The same-package internal test archive replaces the real package archive.
- xtest may depend on symbols introduced by internal test files.
- Local dependents compiled against the real package may become invalid and
  must be rebuilt.
- This recompilation only needs to cover local packages. Third-party package
  archives remain store artifacts compiled before the test override and are not
  candidates for test-time recompilation.
- Cycle detection matters. Use Go's `recompileForTest` as the semantic
  reference.

Recommended simplification:

- Implement import-path keyed graph substitution.
- Do not try to mirror `cmd/go`'s internal package structures exactly.

Acceptance:

- xtest with transitive local deps compiles and links.
- Introduced import cycles are reported clearly.

## Workstream 6: Explicit Cycle and Substitution Errors

File:

- `go/go2nix/pkg/testrunner/testrunner.go`

Goal:

- Do not let graph-substitution failures surface as opaque compile errors.

Plan:

- If recompilation introduces a cycle involving the package under test, return
  a clear error.
- Include import paths in the error.
- Follow Go/Bazel behavior where practical, but do not block on identical error
  wording.

Expectation:

- This should likely stay simple. Production import cycles are already
  prevented by Go package loading. The main risk here is asserting that the
  test-time graph substitution did not create an invalid dependency loop.

## Workstream 7: Test Source Coverage for `modRoot != "."`

Files:

- `nix/dag/default.nix`
- maybe plugin output from `../go-nix-plugin/cpp/resolve_go_packages.cc`

Problem:

- `mainSrc` is build-oriented.
- Tests may need local replace targets outside `modRoot`.

Recommended approach:

1. Add `testSrc` only when `doCheck = true`.
2. `testSrc` should include:
   - `modRoot`
   - local replace target directories
   - parent directories needed for traversal
3. Use `testSrc` only for checks, not for the main build path.

If this is too much churn for the initial landing, split it into a follow-up,
but keep the design in mind.

## Suggested Sequence

1. Plugin test dependency output.
2. Nix test-only third-party derivations and test importcfg.
3. Hook wiring to pass test importcfg.
4. Runner support for test-only third-party deps.
5. Runner local recompilation for xtest.
6. Explicit cycle handling.
7. Optional `testSrc` for `modRoot != "."`.

Do not start with `testSrc`. The biggest correctness hole is xtest
recompilation.

## Regression Fixtures to Add

Add fixtures for:

1. internal test only
2. xtest only
3. xtest with transitive local deps
4. test-only third-party dep
5. internal + xtest together
6. package at module root
7. subdirectory package under `cmd/...` or `internal/...`
8. test using `//go:embed` from same-package test or xtest sources
9. xtest importing a local helper package used only by tests
10. `modRoot != "."`
11. package with no tests

Each fixture should exercise:

- `doCheck = true`
- `doCheck = false`

The embed fixture is important because test embed files must be available
relative to the test source directory, not just production source layout.

## Constraints to Preserve

- When `doCheck = false`, avoid introducing test-only package derivations or
  direct test fan-in.
- Keep Nix declarative and let Go own test graph logic.
- Do not silently ignore xtest recompilation requirements.
- Avoid regressing the existing build graph optimization around `depsImportcfg`.

## Decision Points

1. Plugin output schema
   - Keep test data separate from build data.
   - Prefer additive attrs.

2. Test importcfg bundling
   - Separate bundle preferred.
   - Avoid unconditional test deps in normal builds.

3. Recompilation implementation
   - Import-path keyed graph is sufficient.
   - No need to mirror `cmd/go` internals exactly.

4. `modRoot != "."`
   - Can be deferred until core test correctness lands.
   - Do not remove the current limitation comment until it is actually fixed.

## Follow-up: Override Validation for Test-Only Packages

`testPackages` in `default.nix` reads `packageOverrides` the same way normal
`packages` does, but it does not go through the same validation path that
catches unknown override keys or typos. Add the same override validation to
`testPackages` that normal third-party and local packages already enforce, so
override typos for test-only dependencies are caught consistently.

Priority: low. Test-only third-party packages rarely need overrides in practice
(they're typically `testify`, `go-cmp`, etc.), but consistency matters.

## First Reviewable PR Order

If splitting this into smaller PRs, the clean order is:

1. Plugin output for test-only dependencies.
2. Nix wiring for test-only third-party archives and test importcfg.
3. Go runner changes for xtest recompilation.
4. Optional `testSrc` support for monorepo and local replace layouts.
