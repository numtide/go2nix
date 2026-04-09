# Nix Plugin (`resolveGoPackages`)

Default mode needs to know the Go package graph at **eval time** so it can
turn each package into a separate derivation. Nix has no builtin that can
run `go list`, so go2nix ships a Nix plugin that adds one:
`builtins.resolveGoPackages`.

If you only use experimental mode (`buildGoApplicationExperimental`), you
do not need the plugin — that mode discovers the graph at build time inside
a recursive-nix sandbox.

## What it provides

The plugin registers a single primop:

```nix
builtins.resolveGoPackages {
  go          = "${go}/bin/go";  # path to the Go binary
  src         = ./.;             # source tree
  subPackages = [ "./..." ];     # optional, default ["./..."]
  modRoot     = ".";             # optional
  tags        = [ ];             # optional build tags
  goos        = null;            # optional cross target
  goarch      = null;            # optional cross target
  goProxy     = null;            # optional GOPROXY override
  cgoEnabled  = null;            # optional CGO_ENABLED value
  doCheck     = false;           # also resolve test-only deps
  resolveHashes = false;         # also compute module NAR hashes
}
```

It runs `go list -json -deps` against `src` and returns:

- `packages` — third-party package metadata (`modKey`, `subdir`,
  `imports`, `drvName`, `isCgo`)
- `localPackages` — local package metadata (`dir`, `localImports`,
  `thirdPartyImports`, `isCgo`)
- `modulePath` — the main module's import path
- `replacements` — `replace` directives from `go.mod`
- `testPackages` — test-only third-party packages (when `doCheck = true`)
- `moduleHashes` — module NAR hashes (when `resolveHashes = true`; see
  [Lockfile-free builds](lockfile-format.md#lockfile-free-builds))

You normally never call this directly — `buildGoApplication` does.

## Architecture

The plugin lives under `packages/go2nix-nix-plugin/` and is built in two
halves:

- a **Rust core** (`rust/`) that wraps `go list`, parses its JSON output,
  classifies packages and computes module hashes;
- a **C++ shim** (`plugin/resolveGoPackages.cc`) that registers the primop
  with the Nix evaluator and marshals the Rust output back into Nix values.

Nix's plugin ABI is unstable across releases, so the plugin must be built
against the **same Nix version** you evaluate with. The package defaults to
`nixVersions.nix_2_34`; override `nixComponents` in
`packages/go2nix-nix-plugin/default.nix` to match your Nix.

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

If the plugin is not loaded, evaluating `buildGoApplication` fails with:

```
error: attribute 'resolveGoPackages' missing
```

## Purity

`builtins.resolveGoPackages` is impure: it shells out to `go list`, which
reads `GOMODCACHE` for module sources (and may consult `GOPROXY` for module
metadata). The Nix evaluator does **not** cache its result across
invocations, so the plugin runs once per evaluation.

This is the dominant per-eval cost of default mode; see
[Incremental Builds](incremental-builds.md#the-cost-eval-time) for timings.
