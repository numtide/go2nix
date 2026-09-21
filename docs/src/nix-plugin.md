# Nix Plugin (`resolveGoPackages`)

Default mode needs to know the Go package graph at **eval time** so it can
turn each package into a separate derivation. Nix has no builtin that can
run `go list`, so go2nix ships a Nix plugin that adds one:
`builtins.resolveGoPackages`.

If you only use experimental mode (`buildGoApplicationExperimental`), you
do not need the plugin — that mode discovers the graph at build time inside
a recursive-nix sandbox.

## What it provides

The plugin registers `builtins.resolveGoPackages` (plus a small probe,
`builtins.go2nixApiLevel`, see [API level](#api-level)):

```nix
builtins.resolveGoPackages {
  src         = ./.;             # source tree
  subPackages = [ "." ];         # optional, default [ "." ]
  modRoot     = ".";             # optional
  tags        = [ ];             # optional build tags
  goos        = "linux";         # optional cross target; a string, omit for the host's
  goarch      = "arm64";         # optional cross target; a string, omit for the host's
  goProxy     = null;            # optional GOPROXY override; null inherits the environment
  cgoEnabled  = "0";             # optional CGO_ENABLED; a string, omit for Go's default
  doCheck     = false;           # also resolve what the tests need
  resolveHashes = false;         # also compute module NAR hashes
}
```

`goos`, `goarch` and `cgoEnabled` are strings: leave them out rather than
passing `null`.

The Go toolchain is the plugin's own `pkgs.go`, baked in when the plugin is
built (override `pkgs.go` to use another; it need not be the `go` you hand to
`mkGoEnv`), and it runs with `GOTOOLCHAIN=local`. A `go` attribute is
ignored with a warning, because `go list` runs at evaluation time with the
evaluator's privileges. A source-path `src` carries no derivation context, so
default-mode evaluation stays IFD-free; a derivation-backed `src` is accepted
but emits a warning and forces the plugin to realise the derivation at eval
time — i.e. opt-in IFD, still gated by `allow-import-from-derivation`.

It runs `go list -deps -json` for `subPackages` in `src/modRoot` and, when
`doCheck` is set and the build has local packages, a second
`go list -deps -test` pass over `./...` and every filesystem-replaced
sibling module. It returns:

- `packages` — third-party packages by import path: `modKey`
  (`path@version`, the replacement's version when there is one), `subdir`,
  `imports` (third-party import paths only), `drvName`, `files` (the file
  lists `go list` reported, by kind), and when they apply `isCgo`,
  `cgoPkgConfig`, `cgoCflags`, `cgoLdflags`, and `localImports` (imports
  that resolve to local packages, which happens when the main `go.mod`
  replaces a module the package imports with a directory)
- `localPackages` — packages of the main module and of modules it replaces
  with a directory: `dir` (relative to `src`, `"."` for the root),
  `modPath` (the owning module), `localImports`, `thirdPartyImports`,
  `files`, `mainSrcFiles` (what the final derivation's source must keep
  for this package), and the same cgo fields
- `testPackages`, `testLocalPackages` — the same two shapes for what only
  the tests reach; `testPackages` is `{ }` without `doCheck`, and
  `testLocalPackages` is left out when empty
- `modulePath` — the main module's import path
- `goVersion` — the main module's `go` directive (e.g. `"1.25"`); the dag
  builder threads this as `-lang` to local-package compiles
- `replacements` — for modules replaced by another module
  (`replace a => b v1.2.3`) that own a package in the graph:
  `modKey → { path, version }`.
  Filesystem replaces are not here; they are `siblingModules`
- `siblingModules` — modules replaced with a directory, by module path:
  `path`, `version` (from the `require` line), `goVersion`, `replaceDir`;
  left out when there are none
- `localReplaceDirs`, `nestedModuleRoots` — the replace target directories
  (followed transitively) and every directory under them and `modRoot` that
  holds a `go.mod`, both relative to `src`
- `subPackageClosures` — per main package: `modKeys` and `siblingModPaths`
  (the modules it links, for the embedded module info) and `cxx` (whether
  it needs a C++ linker)
- `moduleHashes` — module NAR hashes (when `resolveHashes = true`; see
  [Lockfile-free builds](lockfile-format.md#lockfile-free-builds))
- `apiLevel` — see below

You normally never call this directly — `buildGoApplication` does.

### API level

The shape above is a contract between the plugin and `nix/dag`. Both carry
a number (`API_LEVEL` in the plugin's Rust core, `apiLevel` in
`nix/dag/default.nix`), the plugin exposes its own as
`builtins.go2nixApiLevel`, and the builder compares the two before calling
the resolver. On a mismatch it prints a warning ("go2nix-nix-plugin: API
level mismatch") and carries on, so the next error you see is usually a
missing attribute: rebuild or reload the plugin from the same revision as
the `nix/` tree you evaluate. An incompatible change to the output bumps
the number on both sides; an additive, optional field does not.

## Architecture

The plugin lives under `packages/go2nix-nix-plugin/` and is built in two
halves:

- a **Rust core** (`rust/`) that wraps `go list`, parses its JSON output,
  classifies packages and computes module hashes;
- a **C++ shim** (`plugin/resolveGoPackages.cc`) that registers the primop
  with the Nix evaluator and marshals the Rust output back into Nix values.

The shim uses Nix's C++ API, which is unstable across releases, so the
plugin must be built against the **same Nix version** you evaluate with.
The package builds against `nixVersions.nix_2_34`; to target another Nix,
change the `nixComponents` binding in
`packages/go2nix-nix-plugin/default.nix`. The build requires Nix 2.34 or
newer, which makes that the minimum for default mode as a whole.

## Loading the plugin

Build it from this flake:

```bash
nix build github:numtide/go2nix#go2nix-nix-plugin
```

Then make the evaluator load it. Either set it globally in `nix.conf`:

```
plugin-files = /nix/store/.../lib/nix/plugins/libgo2nix_plugin.so
```

or pass it per-invocation:

```bash
nix build --option plugin-files /nix/store/.../lib/nix/plugins/libgo2nix_plugin.so .#my-app
```

The latter is what the [bench-incremental](benchmarking.md) harness does
internally.

### Loading the plugin from a flake

Rather than hand-pasting a store path, derive it from the flake input.

On NixOS:

```nix
{ inputs, pkgs, ... }: {
  nix.settings.plugin-files = [
    "${inputs.go2nix.packages.${pkgs.system}.go2nix-nix-plugin}/lib/nix/plugins/libgo2nix_plugin.so"
  ];
}
```

Ad-hoc on the command line:

```bash
nix build .#my-app \
  --option plugin-files \
  "$(nix build --no-link --print-out-paths github:numtide/go2nix#go2nix-nix-plugin)/lib/nix/plugins/libgo2nix_plugin.so"
```

If the plugin is not loaded, evaluating `buildGoApplication` first warns
about an API level mismatch (the resolver's level reads as 0) and then fails
with:

```
error: attribute 'resolveGoPackages' missing
```

To check from the command line: `nix eval --expr 'builtins ? resolveGoPackages'`.

## Purity

`builtins.resolveGoPackages` is impure: it runs `go list`. The Nix
evaluator does **not** cache its result, so it runs on every evaluation,
once per `buildGoApplication` call — twice with `doCheck`, which is the
builder's default.

What `go list` sees is fixed by the plugin, not by your shell. The
environment is cleared and only `GOMODCACHE`, `GOPATH`, `HOME`, `GOPROXY`
and `NETRC` are passed through (`goProxy` overrides `GOPROXY`), plus `PATH`,
`TMPDIR` and the certificate variables unless `goProxy = "off"`. It then
sets `GOFLAGS=-mod=readonly`, `GOENV=off`, `GOWORK=off`, `GOTOOLCHAIN=local`
and `GONOSUMCHECK=*`, and `GOOS`/`GOARCH`/`CGO_ENABLED` from the arguments.
Everything else — `GOPRIVATE`, `GONOPROXY`, `GOEXPERIMENT`,
`GOFIPS140`, your own `GOFLAGS`, the scope's `goEnv` — does not reach it.
Modules are read from `GOMODCACHE`; one that is missing is downloaded
through `GOPROXY` if the network allows, otherwise `go list` fails and the
error asks you to run `go mod download`.

With `resolveHashes = true` (what `goLock = null` turns on) the plugin also
hashes each module's tree in `GOMODCACHE` and remembers the result on disk,
keyed by the module's `h1:` line in `go.sum`, under
`$XDG_CACHE_HOME/go2nix/nar/` (falling back to `~/.cache`, then `/tmp`).
Only modules the build uses are hashed, and one whose tree is not in
`GOMODCACHE` is skipped; the build then fails when it looks that module's
hash up.

This is the dominant per-eval cost of default mode; see
[Incremental Builds](incremental-builds.md#the-cost-eval-time) for timings.
