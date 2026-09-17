# Test Support

go2nix runs Go tests during the check phase of default mode builds. Tests are
compiled and executed per-package, approximating `go test` semantics for
supported cases (see [Limitations](#limitations) below).

## Enabling tests

Tests are controlled by `doCheck`:

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  doCheck = true;
}
```

`doCheck` defaults to `true` (matching `buildGoModule`). The filtered
`mainSrc` for the final derivation includes local replace targets outside
`modRoot`, so test discovery works for sibling-replace layouts without
overrides. See the [Builder API](builder-api.md) table for the other
`buildGoApplication` defaults.

## What gets tested

The test runner runs the tests of the local packages that are part of the
build: the packages `subPackages` reach, including packages of sibling
modules behind a filesystem `replace`, plus local helper packages that only
their tests import. A package with `_test.go` files that nothing in
`subPackages` reaches is skipped (the test binary could not be linked
without compiling it, and nothing else asked for it). Third-party packages
are not tested.

Each testable package goes through these steps:

1. **Internal test compilation** — library source files + `_test.go` files
   in the same package are compiled together into a single archive that
   replaces the library archive.
1. **Dependent recompilation** — local packages that transitively depend on
   the package under test are recompiled against the test archive so the
   dependency graph stays consistent (otherwise the xtest would link two
   copies of the package — one with test helpers, one without).
1. **External test compilation** — `_test.go` files in the `*_test` package
   (xtests) are compiled as a separate package that imports the internal
   test archive.
1. **Test main generation** — a `_testmain.go` is generated that registers
   all `Test*`, `Benchmark*`, `Fuzz*`, and `Example*` functions.
1. **Link and run** — the test binary is linked and executed in the package's
   source directory.

### Internal tests vs external tests (xtests)

Go has two kinds of test files, both supported:

- **Internal tests** (`package foo`): `_test.go` files in the same package.
  These can access unexported identifiers. They are compiled together with
  the package's regular source files into a single archive.

- **External tests** (`package foo_test`): `_test.go` files in the `_test`
  package. These can only access exported identifiers and test the public
  API. They are compiled as a separate package (`foo_test`) that imports
  `foo`.

When a package has both, the internal test archive replaces the original
library archive, and any local dependents reachable from the xtest's import
graph are recompiled to see the replacement.

## Test-only dependencies

When `doCheck = true`, the [Nix plugin](nix-plugin.md) runs a second
`go list -deps -test` pass
to discover third-party packages that are only reachable through test
imports (e.g., `github.com/stretchr/testify`). These are built as separate
`testPackages` derivations and included in a `testDepsImportcfg` bundle
that is a superset of the build importcfg. The same pass finds local packages
that only tests import (an `internal/testutil`, say): they get their own
compile derivations like any other local package, and their own tests run
too.

Test-only dependencies do not touch the per-package compile derivations or
the build importcfg. The check phase, however, is part of the same
derivation that links the binary, so bumping a test-only dependency changes
that derivation: it relinks and re-runs the tests.

## `//go:embed` in tests

Embed directives in test files are supported:

- `TestEmbedPatterns` (from internal `_test.go` files) are resolved and
  their files are symlinked into the internal test source directory alongside
  the package's regular embed files. The embed configs are merged.

- `XTestEmbedPatterns` (from external `_test.go` files) are resolved and
  symlinked into the xtest source directory with their own embed config.

## `extraMainSrcFiles`

Tests run against a filtered copy of `src` that keeps only what the build
needs: for every local package in the build, the files `go list` reports
(`.go` including `_test.go`, assembly, C/C++ and header files, `.syso`),
its resolved `//go:embed` targets and its `testdata/` directory; plus
`go.mod`/`go.sum` at `modRoot` and at the root of each replaced sibling
module. `srcFilter` applies on top. Anything else is dropped so unrelated
edits don't invalidate the test derivation. (With `doCheck = false` the copy
shrinks to the main packages.)

A test that reads a file at runtime *without* `//go:embed` and *outside*
`testdata/` — e.g. `os.ReadFile("../config.yaml")` — will not find it.
Prefer moving such fixtures under `testdata/`. When that isn't practical,
list the paths in `extraMainSrcFiles`:

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  extraMainSrcFiles = [ "config.yaml" "internal/svc/fixtures" ];
}
```

Each entry is relative to `src` (not `modRoot`). A directory entry includes
its full subtree, and a trailing `/` is tolerated. An entry that does not
exist under `src` fails evaluation.

## `checkFlags`

Extra flags passed to the test binary (not to `go test`, since go2nix
compiles and runs tests directly):

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  checkFlags = [ "-test.v" "-test.count=1" ];
}
```

The flags are handed to the test binary as they are, so they take the
`testing` package's own spelling: `-test.v`, `-test.run`, `-test.count`,
`-test.bench`, `-test.timeout`. The short forms (`-v`, `-run`) are `go test`
rewrites and the binary rejects them.

## Limitations

- **Default mode only.** The experimental builder does not run tests.
- **No per-package test caching.** All local tests re-run whenever the final
  app derivation rebuilds; go2nix does not skip individual test packages
  whose inputs are unchanged (unlike `go test`'s cache).
- **Third-party tests are not run**, and neither are local packages outside
  the `subPackages` closure (see [What gets tested](#what-gets-tested)).
- **The working directory is read-only.** Each test runs in its package's
  directory inside the filtered source copy, which is a store path: a test
  that writes next to its sources fails. Use `t.TempDir()`.
- **No `go test` extras.** No coverage instrumentation, no `vet` pass, no
  default 10-minute timeout, and packages are tested one after another.
- **`ldflags` do not reach test binaries**; they are linked with the
  importcfg only.
