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

`doCheck` defaults to `true` when `modRoot == "."` and `false` otherwise.
When `modRoot` points to a subdirectory, the source tree filtered for the
final derivation may not include local replace targets outside the module
root, causing test discovery to fail. Override with `doCheck = true` if your
layout doesn't use out-of-tree replaces.

## What gets tested

The test runner discovers all local packages (under `modRoot`) that contain
`_test.go` files and runs their tests. Third-party packages are not tested.

Each testable package goes through these steps:

1. **Internal test compilation** — library source files + `_test.go` files
   in the same package are compiled together into a single archive that
   replaces the library archive.
1. **Dependent recompilation** — local packages that transitively depend on
   the package under test are recompiled against the test archive so the
   dependency graph stays consistent (Go's "recompileForTest" logic).
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

When `doCheck = true`, the plugin runs a second `go list -deps -test` pass
to discover third-party packages that are only reachable through test
imports (e.g., `github.com/stretchr/testify`). These are built as separate
`testPackages` derivations and included in a `testDepsImportcfg` bundle
that is a superset of the build importcfg.

This means test-only dependencies don't affect the build derivation or its
cache key — they only appear in the check phase.

## `//go:embed` in tests

Embed directives in test files are supported:

- `TestEmbedPatterns` (from internal `_test.go` files) are resolved and
  their files are symlinked into the internal test source directory alongside
  the package's regular embed files. The embed configs are merged.

- `XTestEmbedPatterns` (from external `_test.go` files) are resolved and
  symlinked into the xtest source directory with their own embed config.

## `checkFlags`

Extra flags passed to the test binary (not to `go test`, since go2nix
compiles and runs tests directly):

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  checkFlags = [ "-v" "-count=1" ];
}
```

These map to the standard `testing` package flags (`-v`, `-run`, `-count`,
`-bench`, `-timeout`, etc.).

## Limitations

- **Default mode only.** The experimental builder does not run tests.
- **`modRoot != "."`** disables tests by default. The source filter for the
  final derivation may exclude sibling modules needed by tests.
- **No test caching.** Tests run fresh on every build (there is no
  persistent test cache across derivations).
- **Third-party tests are not run.** Only local packages under the module
  root are tested.
