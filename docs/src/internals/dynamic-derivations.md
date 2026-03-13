# Dynamic Derivations

Dynamic mode uses recursive-nix and content-addressed (CA) derivations to
build Go applications with per-package caching granularity — without needing a
`[pkg]` section in the lockfile. The package graph is discovered at build time
via `go list`, then registered as CA derivations via `nix derivation add`. Nix
builds the full DAG externally with full parallelism.

## Status

All code is implemented across Go (`pkg/nixdrv/`, `pkg/resolve/`,
`cmd/go2nix/resolve.go`) and Nix (`nix/dynamic/default.nix`, `nix/scope.nix`).

What remains is integration testing that requires a Nix daemon with
`recursive-nix`, `ca-derivations`, and `dynamic-derivations` experimental
features enabled. See [Dynamic Derivations — TODO](dynamic-derivations-todo.md)
for the testing checklist and known limitations.

## Requirements

- Nix >= 2.34
- Experimental features: `dynamic-derivations`, `ca-derivations`, `recursive-nix`
- `nixPackage` must be set in `mkGoEnv`

When these aren't available, `buildGoApplication` automatically falls back to
DAG mode.

## Architecture

```
go2nix generate --mode=dynamic → go2nix.toml [mod] only
Nix eval creates ONE recursive-nix wrapper derivation (text-mode CA)
Build time: go2nix resolve →
  1. create + build module FODs from [mod] hashes (only nix build inside wrapper)
  2. set up GOMODCACHE (temp dir with symlinks into FODs)
  3. go list -json -deps → discover package graph
  4. nix derivation add per package (CA nar) — NO build inside wrapper
  5. nix derivation add for link/collector — NO build inside wrapper
  6. copy final .drv file to $out
Nix builds the entire package graph externally (full parallelism)
builtins.outputOf exposes the final binary at eval time

Fallback: DAG mode is used when Nix lacks dynamic-derivations
```

### Why `[mod]` must remain

Module FODs need pre-known NAR hashes. Go's `go.sum` uses tree hashes (not
NAR), so we can't derive them. The `[mod]` section stays but is small and only
changes when `go.mod` changes.

### Why packages are NOT built inside the wrapper

Unlike nix-ninja (which builds inside the wrapper because C/C++ has dynamic
header dependencies discovered at compile time via `-MD`), Go's import graph is
fully known after `go list`. We only need `nix derivation add` to register
package derivations in the Nix store — Nix builds them externally when
resolving `builtins.outputOf`. This gives Nix full control over build
parallelism across all available cores.

The only `nix build` calls inside the wrapper are for module FODs, which must
be materialized so `go list` can read their source files.

## Implementation

### Nix derivation library (`pkg/nixdrv/`)

Go equivalent of nix-ninja's `nix-libstore` + `nix-tool` crates.

| File | Purpose |
|------|---------|
| `github.com/nix-community/go-nix/pkg/storepath` | `StorePath` type (external dependency) — validates `/nix/store/<32-char-hash>-<name>` format |
| `go/go2nix/pkg/nixdrv/derivation.go` | `Derivation` struct matching `nix derivation add` v4 JSON format — builder pattern: `NewDerivation()`, `.AddArg()`, `.SetEnv()`, `.AddCAOutput()`, `.AddInputDrv()`, `.ToJSON()` with sorted keys |
| `go/go2nix/pkg/nixdrv/placeholder.go` | SHA256-based placeholder generation — must match nix-ninja's algorithm exactly |
| `go/go2nix/pkg/nixdrv/tool.go` | `NixTool` — wraps `nix derivation add` (pipe JSON to stdin), `nix build --print-out-paths`, `nix store add` |

#### Derivation JSON format

Matching `nix derivation add` v4 input format (Nix 2.34+). The v4 format uses `"version": 4`, nested `"inputs": { "srcs": [...], "drvs": {...} }`, and store basenames (without `/nix/store/` prefix) for `srcs` and `drvs` keys:

```json
{
  "name": "gopkg-golang.org-x-crypto-ssh",
  "version": 4,
  "outputs": {
    "out": { "hashAlgo": "sha256", "method": "nar" }
  },
  "inputs": {
    "srcs": ["abc123-go2nix-0-unstable"],
    "drvs": {
      "def456-gopkg-curve25519.drv": {
        "outputs": ["out"],
        "dynamicOutputs": {}
      }
    }
  },
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "<build-script>"],
  "env": {
    "GOPROXY": "off",
    "HOME": "/homeless-shelter",
    "out": "/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9"
  }
}
```

Key fields for different derivation types:

- **CA output (floating):** `"out": {"hashAlgo": "sha256", "method": "nar"}` — for package compilations and link (2 keys: `hashAlgo` + `method`)
- **FOD (fixed-output):** `"out": {"method": "nar", "hash": "<sri-hash>"}` — for module fetches (2 keys: `method` + `hash`; the `hash` field makes it fixed-output, Nix allows network access; no `hashAlgo` key)

The `env` map must include `"out"` set to the placeholder string for the `out` output. `nix derivation add` reads JSON from stdin and prints the resulting `.drv` store path on stdout.

Note: the wrapper derivation itself (text-mode CA) is created by Nix eval, not by `nix derivation add`. Only the inner derivations (FODs, packages, link) are created dynamically.

#### Placeholder algorithm

Three types, matching nix-ninja (`nix-libstore/src/placeholder.rs`) and confirmed against Nix source (`src/libstore/downstream-placeholder.cc`):

**Standard** — for simple (non-CA) derivation outputs:

```
SHA256("nix-output:<output_name>") → nix-base32
```

**CA (`unknownCaOutput`)** — for content-addressed derivation outputs (what we use for packages):

```
drv_name = strip ".drv" suffix from store path name
output_path_name = drv_name if output == "out", else drv_name + "-" + output
SHA256("nix-upstream-output:<drv_hash_part>:<output_path_name>") → nix-base32
```

**Dynamic (`unknownDerivation`)** — for outputs of dynamically-created derivations:

```
compressed = XOR-compress(placeholder.hash, 20 bytes)
SHA256("nix-computed-output:<nix-base32(compressed)>:<output_name>") → nix-base32
```

XOR compression: `result[i % new_size] ^= hash[i]` for each byte.

Rendered as: `/<nix-base32-of-hash>` (52 chars, looks like a store path without `/nix/store`).

**Test vectors** (from nix-ninja):

| Type | Input | Result |
|------|-------|--------|
| Standard | `output_name = "out"` | `/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9` |
| CA | `drv = /nix/store/g1w7hy3qg1w7hy3qg1w7hy3qg1w7hy3q-foo.drv`, `output = "out"` | `/0c6rn30q4frawknapgwq386zq358m8r6msvywcvc89n6m5p2dgbz` |
| Dynamic | CA placeholder of above, `output = "out"` | `/0gn6agqxjyyalf0dpihgyf49xq5hqxgw100f0wydnj6yqrhqsb3w` |

#### NixTool operations

Wraps the `nix` CLI (pattern from `nix-tool/src/lib.rs`):

```go
type NixTool struct {
    NixBin string   // path to nix binary
    ExtraArgs []string // e.g. ["--extra-experimental-features", "..."]
}

// DerivationAdd pipes JSON to `nix derivation add`, returns .drv store path
func (t *NixTool) DerivationAdd(drv *Derivation) (StorePath, error)

// Build runs `nix build <installable> --print-out-paths`, returns output paths
func (t *NixTool) Build(installables ...string) ([]StorePath, error)

// StoreAdd runs `nix store add --name <name> <path>`, returns store path
func (t *NixTool) StoreAdd(name, path string) (StorePath, error)
```

`DerivationAdd` implementation: serialize `drv.ToJSON()`, pipe to `nix derivation add` stdin, read stdout for the `.drv` path. On failure, include stderr and the derivation JSON in the error for debugging (nix-ninja pattern).

#### Name sanitization

Derivation names from Go import paths. Valid characters `[a-zA-Z0-9+-._?=]` are preserved; `/` → `-`, `@` → `_at_`, other illegal characters → `_`.

Examples:

- `golang.org/x/crypto/ssh` → `gopkg-golang.org-x-crypto-ssh`
- `github.com/foo/bar@v1.2.3` → `gomod-github.com-foo-bar_at_v1.2.3`

Prefix `gopkg-` for package derivations, `gomod-` for module FODs, `golink-` for link derivations, `gocollect-` for collector derivations.

#### Module path escaping

Go's GOMODCACHE uses case-escaped paths: uppercase letters become `!` + lowercase. This matches `golang.org/x/mod/module.EscapePath()` and the existing `helpers.nix` `escapeModPath`.

### Resolve orchestrator (`pkg/resolve/`)

Runs inside the recursive-nix wrapper at build time.

| File | Purpose |
|------|---------|
| `go/go2nix/pkg/resolve/resolve.go` | Main `Resolve(cfg Config) error` — orchestrates the full flow |
| `go/go2nix/pkg/resolve/graph.go` | `ResolvedPkg` type, `buildPackageGraph()`, `topoSort()` |
| `go/go2nix/pkg/resolve/builder.go` | Generates bash builder scripts for package/link derivations |

#### Resolve flow

```
 Inside wrapper (recursive-nix sandbox):
 ────────────────────────────────────────
 1. Read lockfile [mod] + [replace]
 2. nix derivation add ──→ create FOD per module (with hash from lockfile)
 3. nix build FODs ──────→ materialize modules in /nix/store (ONLY nix build in wrapper)
 4. Set up GOMODCACHE ───→ temp dir, cp -rs each FOD into merged tree
 5. go list -json -deps ─→ discover ALL packages (third-party + local)
 6. Topo-sort packages
 7. nix derivation add ──→ create CA derivation per package (NO build)
 8. nix derivation add ──→ create link derivation (NO build)
 9. nix derivation add ──→ create collector derivation if multiple binaries (NO build)
 10. Copy final .drv FILE to $out

 After wrapper completes (Nix scheduler):
 ──────────────────────────────────────────
 11. builtins.outputOf triggers Nix to read .drv from wrapper output
 12. Nix builds the .drv → transitively builds all package CAs → uses FODs from store
 13. Placeholder resolves to final binary store path
```

**Step 1 — Read lockfile:**

```go
lock, err := lockfile.Read(lockfilePath)
// lock.Mod: map[string]string  e.g. "golang.org/x/crypto@v0.17.0" → "sha256-abc..."
// lock.Replace: map[string]string  e.g. "old/path@v1" → "new/path"
```

**Step 2 — Create module FODs:**

For each module in `lock.Mod`, create a FOD matching `fetch-go-module.nix`:

```json
{
  "name": "gomod-golang.org-x-crypto_at_v0.17.0",
  "version": 4,
  "outputs": {
    "out": {
      "method": "nar",
      "hash": "sha256-abc..."
    }
  },
  "inputs": {
    "srcs": ["abc123-go", "def456-cacert"],
    "drvs": {}
  },
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "export HOME=$TMPDIR\nexport GOMODCACHE=$out\nexport GOSUMDB=off\nexport GONOSUMCHECK='*'\n/nix/store/.../bin/go mod download \"golang.org/x/crypto@v0.17.0\""],
  "env": {
    "out": "<standard-placeholder>"
  }
}
```

The `hash` field makes this a FOD — Nix allows network access. The builder uses `GOMODCACHE=$out` so `go mod download` writes directly to the output. The NAR hash must match the lockfile entry (same computation as `modCacheHash` in `generate.go`).

For replaced modules, use `fetchPath` from `lock.Replace` instead of the module path.

**Step 3 — Materialize modules:**

```go
// Build all FODs in a single batched nix build call.
// Nix handles parallelism internally via --max-jobs.
installables := make([]string, 0, len(fodDrvPaths))
for _, drvPath := range fodDrvPaths {
    installables = append(installables, drvPath.Absolute()+"^out")
}
paths, err := nix.Build(installables...)
if err != nil { return fmt.Errorf("building FODs: %w", err) }
```

**Step 4 — Set up GOMODCACHE:**

Each FOD output IS a GOMODCACHE subtree (because `GOMODCACHE=$out`). Its structure:

```
/nix/store/...-gomod-golang-org-x-crypto-v0-17-0/
  cache/download/golang.org/x/crypto/@v/v0.17.0.{zip,mod,info,ziphash,lock}
  golang.org/x/crypto@v0.17.0/
    go.mod
    *.go files
```

Source files live at `${fod}/${escapeModPath(fetchPath)}@${version}/`.

Merge all FODs into a single GOMODCACHE using recursive symlink copy:

```go
gomodcache, _ := os.MkdirTemp("", "gomodcache-")
for modKey, fodPath := range fodPaths {
    // cp -rs creates real directories, symlinks at leaf level
    // Merges correctly since each FOD has different module paths
    exec.Command("cp", "-rs", fodPath.String()+"/.", gomodcache).Run()
}
```

This is ephemeral — only used for `go list`, never stored in the Nix store.

**Step 5 — Discover packages:**

```go
cmd := exec.Command(gobin, "list", "-json", "-deps", "-tags", tags, subPackages...)
cmd.Dir = srcPath
cmd.Env = append(os.Environ(),
    "GOMODCACHE=" + gomodcache,
    "GONOSUMCHECK=*",
    "GOPROXY=off",       // no network, everything is local
    "GOFLAGS=-mod=mod",
)
```

Returns ALL packages (third-party + local). Filter: `pkg.Standard` → skip, `pkg.Module.isLocal()` → local package, else → third-party.

**Step 6 — Topological sort:**

```go
type ResolvedPkg struct {
    ImportPath   string
    ModKey       string          // "" for local packages
    GoFiles      []string
    CgoFiles     []string
    Imports      []string        // non-stdlib import paths
    IsLocal      bool
    FodPath      StorePath       // FOD output path (third-party)
    FetchPath    string          // escaped fetch path for source lookup within FOD
    Version      string
    Subdir       string          // package path relative to module root
}

// topoSort returns packages in dependency order (leaves first)
func topoSort(pkgs map[string]*ResolvedPkg) ([]*ResolvedPkg, error)
```

**Step 7 — Create package CA derivations:**

For each package, `nix derivation add` only (NO `nix build`):

```json
{
  "name": "gopkg-golang.org-x-crypto-ssh",
  "version": 4,
  "outputs": {
    "out": {"hashAlgo": "sha256", "method": "nar"}
  },
  "inputs": {
    "srcs": [
      "abc123-gomod-golang.org-x-crypto_at_v0.17.0",
      "def456-go",
      "ghi789-go2nix",
      "jkl012-stdlib"
    ],
    "drvs": {
      "mno345-gopkg-curve25519.drv": {"outputs": ["out"], "dynamicOutputs": {}},
      "pqr678-gopkg-internal-poly1305.drv": {"outputs": ["out"], "dynamicOutputs": {}}
    }
  },
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "<compile-script>"],
  "env": {
    "importPath": "golang.org/x/crypto/ssh",
    "modSrc": "/nix/store/...-gomod-golang.org-x-crypto_at_v0.17.0",
    "relDir": "golang.org/x/crypto@v0.17.0/ssh",
    "importcfg_entries": "packagefile crypto/aes=/nix/store/.../std/crypto/aes.a\npackagefile golang.org/x/crypto/curve25519=<ca-placeholder-for-curve25519>",
    "out": "<ca-placeholder>"
  }
}
```

The `relDir` is computed as `${escapeModPath(fetchPath)}@${version}/${subdir}`.

The compile script:

```bash
set -euo pipefail
export HOME=$TMPDIR
mkdir -p $out

# Write importcfg from env var (placeholders resolved by Nix at build time)
printf '%s\n' "$importcfg_entries" > $NIX_BUILD_TOP/importcfg

# Source files live inside the FOD's GOMODCACHE layout
srcdir="$modSrc/$relDir"

# Compile
go2nix compile-package \
  --import-path "$importPath" \
  --import-cfg $NIX_BUILD_TOP/importcfg \
  --src-dir "$srcdir" \
  --output "$out/pkg.a" \
  --trim-path "$NIX_BUILD_TOP" \
  ${pflag:+--p "$pflag"} \
  ${tags:+--tags "$tags"} \
  ${gcflags:+--gc-flags "$gcflags"}
```

For **local packages**, `modSrc` is the `--src` store path and `relDir` is the package path relative to the module root.

**importcfg_entries construction:**

For each import of a package:

- **Stdlib**: `packagefile <import>=${stdlib}/<import>.a` — real paths, stdlib is a static input
- **Third-party/local dep**: `packagefile <import>=<ca-placeholder>/pkg.a` — where `<ca-placeholder>` is `Placeholder.CAOutput(depDrvPath, "out").Render()`

The CA placeholder for a dependency is computed from the dependency's `.drv` path (returned by `nix derivation add`). At build time, Nix substitutes the placeholder with the real output path.

**Step 8 — Create link derivation:**

One link derivation per main package (packages where `Name == "main"`):

```json
{
  "name": "golink-myapp",
  "version": 4,
  "outputs": {
    "out": {"hashAlgo": "sha256", "method": "nar"}
  },
  "inputs": {
    "srcs": ["abc123-go", "def456-coreutils", "ghi789-stdlib"],
    "drvs": {
      "jkl012-gopkg-main.drv": {"outputs": ["out"], "dynamicOutputs": {}},
      "mno345-gopkg-dep1.drv": {"outputs": ["out"], "dynamicOutputs": {}},
      "...all-transitive-deps.drv": {"outputs": ["out"], "dynamicOutputs": {}}
    }
  },
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "<link-script>"],
  "env": {
    "mainPkg": "<ca-placeholder-for-main-pkg>",
    "importcfg_entries": "packagefile main=<ca-placeholder-for-main>/pkg.a\npackagefile github.com/foo/bar=<ca-placeholder-for-bar>/pkg.a\n...",
    "ldflags": "-s -w -X main.version=1.0",
    "out": "<ca-placeholder>"
  }
}
```

The link script:

```bash
set -euo pipefail
mkdir -p $out/bin

# Write importcfg for all transitive dependencies
printf '%s\n' "$importcfg_entries" > importcfg

# Link
go tool link -o $out/bin/${pname} -importcfg importcfg \
  -buildmode=exe ${ldflags} ${mainPkg}/pkg.a
```

**Step 9 — Create collector derivation (multiple binaries):**

If `subPackages` produces multiple main packages, create a collector:

```json
{
  "name": "gocollect-myapp",
  "version": 4,
  "outputs": {
    "out": {"hashAlgo": "sha256", "method": "nar"}
  },
  "inputs": {
    "srcs": ["abc123-bash", "def456-coreutils"],
    "drvs": {
      "ghi789-golink-cmd1.drv": {"outputs": ["out"], "dynamicOutputs": {}},
      "jkl012-golink-cmd2.drv": {"outputs": ["out"], "dynamicOutputs": {}}
    }
  },
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "mkdir -p $out/bin\ncp <link1-placeholder>/bin/* $out/bin/\ncp <link2-placeholder>/bin/* $out/bin/"],
  "env": {
    "out": "<ca-placeholder>",
    "PATH": "/nix/store/.../coreutils/bin"
  }
}
```

If there's only one main package, skip the collector and use the link derivation directly.

**Step 10 — Copy .drv file to $out:**

```go
// Get the final .drv path (collector if multiple binaries, link if single)
finalDrvPath := collectorDrvPath // or linkDrvPath

// Copy the .drv FILE to $out (not a path string — the actual .drv file content)
// This is how nix-ninja does it: fs::copy(drv_store_path, $out)
// Nix recognizes the text-mode output as a derivation and builds it
// when builtins.outputOf is resolved.
if err := copyFile(finalDrvPath.String(), outputPath); err != nil {
    return fmt.Errorf("writing output: %w", err)
}
```

**Critical**: `$out` receives the `.drv` FILE content (binary copy), not a path string. nix-ninja does this with `fs::copy(derived_file.derived_path.store_path().path(), out)`. Nix's dynamic derivation mechanism recognizes the text-mode output as a `.drv` and builds it when `builtins.outputOf` is resolved.

### CLI (`cmd/go2nix/resolve.go`)

The `resolve` subcommand with flags: `--src`, `--lockfile`, `--system`, `--go`,
`--stdlib`, `--nix`, `--go2nix`, `--bash`, `--coreutils`, `--pname`,
`--sub-packages`, `--tags`, `--ldflags`, `--gcflags`, `--cgo-enabled`,
`--overrides`, `--cacert`, `--netrc-file`, `--output`.

### Nix wrapper (`nix/dynamic/default.nix`)

```nix
{ lib, stdenv, go, go2nix, nixPackage, coreutils, bash, cacert, netrcFile, stdlib }:

{ pname
, src
, goLock
, subPackages ? [ "." ]
, tags ? []
, ldflags ? []
, gcflags ? []
, CGO_ENABLED ? null
, nativeBuildInputs ? []
, moduleDir ? "."
, packageOverrides ? {}
, ...
}:

assert lib.assertMsg (builtins ? outputOf) "...requires dynamic-derivations...";
assert lib.assertMsg (lib.versionAtLeast ... "2.34") "...requires Nix >= 2.34...";

let
  moduleRoot = if moduleDir == "." then "${src}" else "${src}/${moduleDir}";

  overridesJSON = builtins.toJSON (lib.mapAttrs (_path: cfg: {
    nativeBuildInputs = map toString (...);
  }) packageOverrides);

  wrapperDrv = stdenv.mkDerivation {
    name = "${pname}.drv";

    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";
    requiredSystemFeatures = [ "recursive-nix" ];
    NIX_NO_SELF_RPATH = true;

    nativeBuildInputs = [
      go go2nix nixPackage coreutils bash cacert
    ] ++ lib.concatMap (cfg: cfg.nativeBuildInputs or [])
         (lib.attrValues packageOverrides)
    ++ nativeBuildInputs;

    dontUnpack = true;
    dontInstall = true;
    dontFixup = true;

    buildPhase = ''
      export NIX_CONFIG="extra-experimental-features = nix-command ca-derivations dynamic-derivations"
      export HOME=$TMPDIR

      go2nix resolve \
        --src ${moduleRoot} \
        --lockfile ${goLock} \
        --system ${stdenv.hostPlatform.system} \
        --go ${go}/bin/go \
        --stdlib ${stdlib} \
        --nix ${nixPackage}/bin/nix \
        --go2nix ${go2nix}/bin/go2nix \
        --bash ${bash}/bin/bash \
        --coreutils ${coreutils}/bin/mkdir \
        --pname ${lib.escapeShellArg pname} \
        --sub-packages ${lib.escapeShellArg (lib.concatStringsSep "," subPackages)} \
        --tags ${lib.escapeShellArg (lib.concatStringsSep "," tags)} \
        --ldflags ${lib.escapeShellArg (lib.concatStringsSep " " ldflags)} \
        --overrides ${lib.escapeShellArg overridesJSON} \
        ${lib.optionalString (CGO_ENABLED != null) "--cgo-enabled ${toString CGO_ENABLED}"} \
        ${lib.optionalString (gcflags != []) "--gcflags ${lib.escapeShellArg (lib.concatStringsSep " " gcflags)}"} \
        --cacert ${cacert}/etc/ssl/certs/ca-bundle.crt \
        ${lib.optionalString (netrcFile != null) "--netrc-file ${netrcFile}"} \
        --output $out
    '';

    passthru = {
      target = builtins.outputOf wrapperDrv.outPath "out";
      inherit go go2nix goLock;
    };
  };

in wrapperDrv
```

### Feature detection and fallback (`nix/scope.nix`)

```nix
hasDynamicDerivations = builtins ? outputOf;

buildGoApplication =
  if hasDynamicDerivations && nixPackage != null
  then buildGoApplicationDynamicMode
  else buildGoApplicationDAGMode;  # existing path
```

### How `builtins.outputOf` works

`builtins.outputOf` creates a `SingleDerivedPath::Built` value — a nested type that can represent chains of dynamic derivation references.

For our wrapper:

1. **At eval time:** `builtins.outputOf wrapperDrv.outPath "out"` constructs:

   ```
   Built {
     drvPath: Built { drvPath: Opaque{/nix/store/xxx-myapp.drv.drv}, output: "out" },
     output: "out"
   }
   ```

   This is a double-nested `Built`: the "out" output of (the "out" output of the wrapper derivation). The string value is a `DownstreamPlaceholder` (opaque hash).

2. **At build time:** Nix resolves the chain:

   - Builds `xxx-myapp.drv.drv` (the wrapper) → runs `go2nix resolve` → output is a `.drv` file
   - Reads the `.drv` file from the wrapper's text-mode output
   - Builds that `.drv` (the link/collector) → transitively builds all package CAs → uses FODs from store
   - The "out" output of the link/collector is the final binary directory

3. **Placeholder resolution:** The `DownstreamPlaceholder` from step 1 resolves to the final store path (e.g., `/nix/store/abc-golink-myapp/bin/myapp`).

Consumers use `wrapperDrv.passthru.target` to get the binary reference.

## Cgo handling

Packages needing extra `nativeBuildInputs` (pkg-config, libfoo) are handled by `packageOverrides`:

1. `packageOverrides` is serialized to JSON at eval time and passed via `--overrides`
2. All `nativeBuildInputs` from overrides are added to the wrapper's `nativeBuildInputs` (so they're in the sandbox and their store paths are available for package derivations)
3. `go2nix resolve` reads `--overrides` JSON. For matching import paths, adds the store paths to the package derivation's `inputSrcs`, `PATH`, and `PKG_CONFIG_PATH` env vars

Example:

```nix
packageOverrides = {
  "github.com/mattn/go-sqlite3" = {
    nativeBuildInputs = [ pkgs.sqlite pkgs.pkg-config ];
  };
};
```

Becomes in the package derivation's env:

```json
{
  "PATH": "/nix/store/.../sqlite/bin:/nix/store/.../pkg-config/bin:...",
  "PKG_CONFIG_PATH": "/nix/store/.../sqlite/lib/pkgconfig:..."
}
```

And the store paths are added to `inputSrcs` so Nix knows to fetch them when building the package derivation externally.

## Internals

### Parallelism

Inside the wrapper:

- **Module FOD creation**: Sequential `nix derivation add` calls (one per module).
- **Module FOD build**: A single batched `nix build` call with all FOD installables — Nix handles parallelism internally via `--max-jobs`.
- **Package derivation creation**: Sequential `nix derivation add` in topological order. Only registers derivations (fast, no build).

Package compilation parallelism is handled by Nix externally — it builds the full DAG with its own job scheduler (`--max-jobs`). This is more efficient than building inside the wrapper.

### Stdlib handling

The `--stdlib` flag points to the pre-compiled Go standard library archive directory. In the Nix wrapper this is:

```nix
stdlib = buildGoStdlib { inherit go; };  # existing go2nix derivation
```

The stdlib store path is added to `inputSrcs` of every package derivation. In `importcfg_entries`, stdlib packages are referenced as:

```
packagefile fmt=${stdlib}/fmt.a
packagefile net/http=${stdlib}/net/http.a
```

These are real paths (not placeholders) since the stdlib is a static input, not a dynamic derivation.

### Error handling

Following nix-ninja's pattern:

- On `nix derivation add` failure: include stderr + full derivation JSON in error message
- On `nix build` failure: include the `.drv` path and stderr
- On `go list` failure: include stderr (often contains compilation errors)
- All errors are wrapped with context: `fmt.Errorf("creating FOD for %s: %w", modKey, err)`

### Lockfile changes

`go2nix generate --mode=dynamic` skips `[pkg]` collection. The `Pkg` field uses
`omitempty` so TOML omits it when nil. `resolve` reads only `Mod` + `Replace`,
ignores `Pkg`.

## Files summary

| File | Description |
|------|-------------|
| `github.com/nix-community/go-nix/pkg/storepath` | External dependency — StorePath validation |
| `go/go2nix/pkg/nixdrv/derivation.go` | Derivation JSON struct |
| `go/go2nix/pkg/nixdrv/placeholder.go` | Placeholder generation |
| `go/go2nix/pkg/nixdrv/tool.go` | Nix CLI wrapper |
| `go/go2nix/pkg/golist/golist.go` | Extracted go list wrapper |
| `go/go2nix/pkg/resolve/resolve.go` | Resolve orchestrator |
| `go/go2nix/pkg/resolve/graph.go` | Package graph types |
| `go/go2nix/pkg/resolve/builder.go` | Builder script generation |
| `go/go2nix/cmd/go2nix/resolve.go` | CLI subcommand |
| `go/go2nix/cmd/go2nix/main.go` | `resolve` case in command dispatch |
| `nix/dynamic/default.nix` | Recursive-nix wrapper |
| `nix/scope.nix` | Dynamic builder + nixPackage + auto-selection |
| `nix/mk-go-env.nix` | Plumbs nixPackage param |
| `go/go2nix/pkg/lockfilegen/generate.go` | `--mode` flag (dag/dynamic/vendor) |
| `go/go2nix/pkg/lockfile/lockfile.go` | `omitempty` on Pkg field |

## Reference implementations

- **nix-ninja** `nix-libstore/src/derivation.rs` — Derivation struct, JSON serialization with sorted keys
- **nix-ninja** `nix-libstore/src/placeholder.rs` — Placeholder algorithm (standard, CA, dynamic) + test vectors
- **nix-ninja** `nix-tool/src/lib.rs` — `derivation_add()` (pipe stdin), `build()`, `store_add()`
- **nix-ninja** `nix-ninja/src/cli.rs` — `fs::copy(drv_store_path, $out)` for text-mode output
- **nix-ninja** `nix-ninja/src/task.rs` — Thread-based parallelism, `nix_build_lock` mutex, error handling with drv JSON dumps
- **nixnative** `nix/native/ninja/wrapper.nix` — `__contentAddressed`, `outputHashMode = "text"`, `builtins.outputOf`, `NIX_NO_SELF_RPATH`
- **nixnative** `nix/native/builders/api.nix` — `realizeTarget` pattern for consuming `builtins.outputOf` outputs
- **Nix** `src/libexpr/primops.cc` — `prim_outputOf` implementation, `SingleDerivedPath::Built` chain
- **Nix** `src/libstore/downstream-placeholder.cc` — `unknownCaOutput`, `unknownDerivation` placeholder generation
