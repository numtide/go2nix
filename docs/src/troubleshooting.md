# Troubleshooting

## `error: attribute 'resolveGoPackages' missing`

The default-mode builder calls `builtins.resolveGoPackages`, which is
provided by the go2nix Nix plugin. This error means the evaluating Nix
hasn't loaded it.

Load the plugin via `nix.conf` `plugin-files = ...` or
`--option plugin-files <path>`. See [Nix Plugin](nix-plugin.md). Just before
this error Nix also prints a `go2nix-nix-plugin: API level mismatch` warning
with `nix builtin resolver = 0`; that is the same problem seen from the
builder's side. `nix eval --expr 'builtins ? resolveGoPackages'` tells you
whether the plugin is loaded.

If the plugin *is* configured and you still see this, your Nix and the
plugin were built against different `libnixexpr` versions — rebuild the
plugin against the Nix you're evaluating with.

## `packageOverrides.<path>: unknown attributes ["nativeBuildInputs"]`

Full message:

```
packageOverrides.<path>: unknown attributes ["nativeBuildInputs"]. Valid: env, srcOverlay (nativeBuildInputs is cgo-only — rawGoCompile hardcodes PATH)
```

You set `nativeBuildInputs` for a package that go2nix classified as
**non-cgo**. Non-cgo packages use a raw builder that bypasses stdenv, so
`nativeBuildInputs` would have no effect; the builder rejects it instead of
silently ignoring it. (It does so only when the key is that package's exact
import path. Under a module-path key the attribute is simply not applied to
the module's non-cgo packages.)

Fixes:

- If the package really is cgo, make sure `CGO_ENABLED` isn't forced to `0`
  and that the cgo files aren't excluded by build tags on your target
  platform.
- If you need to influence a pure-Go compile, use `env` instead.
- If you only need the inputs at link time, put them in the top-level
  `nativeBuildInputs` of `buildGoApplication` rather than in
  `packageOverrides`.

See [Package Overrides](package-overrides.md).

## Stale lockfile: `attribute '"<module>@<version>"' missing`, or link-binary fails validating `go.mod`

A lockfile that no longer matches `go.mod` shows up in one of two ways.

If a package in the build imports a module (or a version) the lockfile does
not have, evaluation stops before anything is built:

```
error: attribute '"gopkg.in/yaml.v3@v3.0.1"' missing
```

Otherwise default mode still validates the lockfile against `go.mod` at link
time (`mvscheck`), which catches requirements the package graph did not
reach:

```
link-binary failed ... lockfile check: go.mod requires modules not found in lockfile ...
the lockfile is stale; run `go mod tidy && go2nix generate` to update it
```

Either way, regenerate — `generate` reads `go.mod`, so tidy it first:

```bash
go mod tidy && go2nix generate .
```

You do **not** need to regenerate after editing imports between packages
that already exist — the lockfile pins modules, not the package graph. See
[When to regenerate](lockfile-format.md#when-to-regenerate).

Run `go2nix check .` to validate without building. In experimental mode the
same condition reads
`lockfile missing module <module>@<version> — regenerate with go2nix generate`.

## Private modules: 404 or auth failures in module FODs

Module fetch derivations run `go mod download` in a sandbox. In default mode
they inherit `GOPROXY` and `NETRC` from the builder's environment, which a
Nix daemon or a remote builder usually does not have; to make a build
independent of that, set the proxy with `buildGoApplication { goProxy = ...; }`
and pass a `.netrc` via `mkGoEnv { netrcFile = ...; }`. `go2nix generate`
downloads the same modules from your shell and needs the credentials there
too. See
[Private modules](builder-api.md#private-modules-netrcfile) for the format
and the store-path-visibility caveat.

## Evaluation feels slow on large graphs

Every evaluation runs `go list -json -deps` (via the plugin) and
instantiates one derivation per package. On a few-thousand-package graph
this is a few hundred milliseconds of floor on every `nix build`, even when
nothing changed. That's expected; see
[Incremental Builds](incremental-builds.md#the-cost-eval-time).

If *builds* (not eval) cascade further than you expect after small edits,
turn on `contentAddressed = true` so private-symbol changes don't rebuild
reverse dependents. Use [bench-incremental](benchmarking.md) to measure.

## Inspecting the package graph

The default-mode app derivation exposes the graph through `passthru`:

```bash
# All third-party package derivations
nix eval .#my-app.passthru.packages --apply builtins.attrNames

# All local package derivations
nix eval .#my-app.passthru.localPackages --apply builtins.attrNames

# Build a single package in isolation
nix build '.#my-app.passthru.packages."github.com/foo/bar"'

# The bundled importcfg used at link time
nix build .#my-app.passthru.depsImportcfg
```

Also available: `passthru.go`, `passthru.go2nix`, `passthru.goLock`,
`passthru.mainSrc`, `passthru.modulePath`, and (when `doCheck = true`)
`passthru.testPackages` / `passthru.testDepsImportcfg`.
