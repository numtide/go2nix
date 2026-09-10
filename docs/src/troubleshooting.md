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

## `go2nix-nix-plugin: API level mismatch`

```
evaluation warning: go2nix-nix-plugin: API level mismatch.
  nix builtin resolver = 0
  nix/dag/default.nix  = 1
Your nix was built against a different go2nix-nix-plugin revision
than the nix/ tree you are evaluating. Rebuild/reload the plugin
against this checkout.
```

The builder and the plugin each carry a number that is bumped when the data
passed between them changes shape. `resolver = 0` means no plugin answered at
all (the next thing you see is the `resolveGoPackages missing` error above).
Any other pair means the plugin and the `nix/` tree come from different
go2nix revisions: take `go2nix-nix-plugin`, the `go2nix` CLI and `lib.mkGoEnv`
from the same flake input, and rebuild whatever `plugin-files` points at after
updating that input. It is a warning, so evaluation goes on, and what happens
next depends on what changed between the two revisions.

## `resolveGoPackages: 'go list' failed (exit 1).`

The plugin ran `go list` on your module and `go` itself gave up. Its own
message follows on the next lines, and that is the one to read. The trailing
`Hint: ensure all modules are in your local cache` only applies to the first
case below.

- `go: downloading …` followed by a network error, a 404 or a 401: a module
  is not in `GOMODCACHE` and could not be downloaded where Nix evaluates. The
  evaluation-time `go list` inherits `GOMODCACHE`, `GOPATH`, `HOME`,
  `GOPROXY`, `NETRC` and the TLS certificate variables, and nothing else: no
  `GOPRIVATE`, no `GONOSUMDB`, no `HTTPS_PROXY`, and `go env -w` settings do
  not apply (`GOENV=off`). `goProxy` on the build overrides `GOPROXY`. Run
  `go mod download` in the module, or fix the proxy and credentials (see
  [Private modules](#private-modules-404-or-auth-failures-in-module-fods)).
- `go: go.mod requires go >= 1.N (running go 1.M; GOTOOLCHAIN=local)`: the
  `go` line of `go.mod` asks for a newer Go than the one in your scope. Lower
  the directive or use a newer `go` in `mkGoEnv`;
  `nix eval --raw nixpkgs#go.version` tells you what you have.
- `go: errors parsing go.mod`: exactly that.

## `resolveGoPackages: package errors:`

```
error: resolveGoPackages: package errors:
  - example.com/my-app/internal/nope: cannot find module providing package example.com/my-app/internal/nope: import lookup disabled by -mod=readonly
Hint: your GOMODCACHE may be stale. Run 'go mod download' to populate it.
```

`go list` ran, but some packages came back with errors; each line is one
package and what `go` said about it.
`resolveGoPackages: test dependency errors:` is the same thing from the
second pass, which looks at test imports when `doCheck` is on. The hint at the end is rarely the cause. In order of
likelihood:

- **A local package that Nix cannot see.** The import path is inside your own
  module and the directory exists on disk, but not in `src`: a new directory
  that is not `git add`-ed yet (flakes only copy tracked files), or one that
  `srcFilter` or a `lib.fileset` leaves out.
- **`go.mod` or `go.sum` is not tidy.**
  `missing go.sum entry for module providing package …`, or
  `cannot find module providing package …` for a third-party import: run
  `go mod tidy`. The plugin runs with `-mod=readonly`, so it never fixes them
  for you.
- **A build constraint excludes every file** of a package for the target
  platform or tag set (`build constraints exclude all Go files in …`): check
  `tags`, `goEnv.GOOS`/`GOARCH` and `CGO_ENABLED`.

## `warning: resolveGoPackages: realising derivation '…' at eval time (IFD)`

`src` is the output of a derivation, typically `pkgs.fetchFromGitHub`. The plugin has to read the source while Nix evaluates,
so Nix must build that derivation first: import from derivation. It works if
`allow-import-from-derivation` is on, but it serialises evaluation behind a
build. Use a path, a flake input, `builtins.fetchGit` or `builtins.fetchTarball`
instead; see [Builder API](builder-api.md#src).

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

## `parsing go2nix.toml: unknown keys: […]`

The lockfile has sections this version of go2nix does not know, which is what
an old-format lockfile looks like (`[pkg]`, for instance). Every Go-side
reader rejects it, `go2nix generate` included, because it reads the existing
file first to reuse its hashes. Delete the file, or write to a new path with
`-o`, and generate again.

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

## `compile manifest: unsupported version N (expected M)`

Also `link manifest: …`, `test manifest: …`, and
`manifest is missing files; rebuild the nix-plugin`. The Nix side writes a
small JSON manifest for each compile, link and test run, and the go2nix CLI
inside the derivation refuses one written for a different format. It means
the CLI (`mkGoEnv { go2nix = …; }`), the `nix/` tree (`go2nix.lib`) and the
plugin are not from the same go2nix revision. Take all three from one flake
input. The `missing files` form specifically means the plugin is older than
the builder: rebuild and reload it.

## Experimental mode

```
go2nix dynamic mode requires the dynamic-derivations experimental feature (which implies ca-derivations). Enable with: extra-experimental-features = dynamic-derivations ca-derivations recursive-nix
```

The evaluating Nix has no `builtins.outputOf`. Enable the features for the
evaluator, not only for the daemon.

```
go2nix dynamic mode requires Nix >= 2.34 (v4 derivation JSON format), got <version>
```

The check is against the `nixPackage` given to `mkGoEnv`, the Nix that runs
inside the wrapper derivation, not against the Nix you are typing commands
into.

```
buildGoApplicationExperimental requires nixPackage to be set in mk-go-env
```

Pass `nixPackage = pkgs.nixVersions.nix_2_34` (or newer) to `mkGoEnv`.

```
packageOverrides.<path>: unknown attributes ["env"]. Experimental mode only supports: nativeBuildInputs
```

The per-package derivations are made at build time by `go2nix resolve`, which
can add inputs to them but not environment variables or source overlays. Use
the default builder if you need `env` or `srcOverlay`.

```
lockfile missing module <module>@<version> — regenerate with go2nix generate
```

In the wrapper's build log: the experimental builder has no lockfile-free
mode and found a module the lockfile does not list. See the stale-lockfile
entry above.

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

[Debugging a Build](debugging.md) is the longer version of this section.

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
