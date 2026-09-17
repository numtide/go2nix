# Tests

What is here, what each kind of test proves, and how to run it. All of these
need the go2nix Nix plugin, so they are Linux and macOS only, and the ones that
run a build inside a derivation need the `recursive-nix` system feature on the
machine that builds them.

## `fixtures/`

Small Go modules, one behaviour each: build tags, assembly, cgo and C++,
`pkg-config`, test-only dependencies, `//go:embed` in tests, nested modules,
sibling modules behind a filesystem `replace`, `modRoot`, `srcFilter`, a
third-party package importing a locally replaced module, and so on. Each has a
`dag.nix` that builds it with the default builder, and most have a
`go2nix.toml`.

Two ways to run one:

```bash
# directly, with the plugin loaded into your own nix
nix-build tests/fixtures/testify-basic/dag.nix \
    --option plugin-files "$(nix build .#go2nix-nix-plugin --no-link --print-out-paths)/lib/nix/plugins/libgo2nix_plugin.so"

# as a flake package: builds it inside a derivation and asserts on the result
nix build .#test-fixture-testify-basic
```

The flake packages are in `packages/test-fixture-<name>/`. They populate a Go
module cache, run `nix-build` on the fixture with the plugin, and then check
what came out (the binary's output, `go version -m`, which derivations were
built). A new fixture needs both: the module under `fixtures/` and a package
that runs it.

`fixtures/torture-project` is a large multi-module repository used by the
benchmarks and by the plugin's evaluation test; `fixtures/light-project` is the
small counterpart.

## `packages/`

Real programs (`yubikey-agent`, `dotool`, `nwg-drawer`, `vinegar`) fetched from
their upstream repositories, with a lockfile and an expression per builder:
`dag.nix` for the default builder, `dynamic.nix` for the experimental one.
They run as `.#test-package-<name>`, `.#test-package-<name>-dynamic` and, for
the lockfile-free path, `.#test-package-yubikey-agent-no-lockfile`.
`packages/test-lib/` in the repository root has the two runners they share.

`tests/default.nix` exposes the same expressions for
`nix-build tests/ -A yubikey-agent.dag`.

## `nix/`

Tests written in Nix. `helpers_test.nix` and `fetch_go_module_test.nix`
evaluate to `true` or throw, and run as `checks.<system>.nix-unit-tests`.
`cross_platform_test.nix` is `checks.<system>.cross-platform-env`. The others
(`mainsrc_replace_test.nix`, `modroot_dotslash_test.nix`,
`nested_module_test.nix`, `testmain_badsig_test.nix`) build a fixture and
assert on derivation paths or on an expected failure; they are the packages
`test-fixture-mainsrc-replace`, `-modroot-dotslash`, `-nested-module` and
`-testmain-badsig`.

## `test-ca.sh`

Starts a throw-away Nix daemon with `ca-derivations` enabled, builds the
`xtest-local-dep` fixture with `contentAddressed = true` in it, and tears it
down. Needs `socat`.

## Elsewhere

- Go unit tests: `go test ./...` in `go/go2nix`; they also run when
  `.#go2nix` is built.
- Rust unit tests of the resolver: `cargo test` in
  `packages/go2nix-nix-plugin/rust`; they also run when
  `.#go2nix-nix-plugin` is built. `packages/go2nix-nix-plugin/tests/` holds
  two evaluation tests of the loaded plugin, which are flake checks.
- Benchmarks: see `docs/src/benchmarking.md`.

## What CI runs

`nix flake check` and the CI build the flake's `checks`: formatting, the
linters, the GODEBUG table check, the Nix unit tests and the two plugin
evaluation tests. The fixture and package tests above are flake *packages*,
not checks, so they do not run on a pull request: build the ones your change
touches by hand, and say which in the pull request.
