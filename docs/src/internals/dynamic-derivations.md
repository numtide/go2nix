# Dynamic Derivations for go2nix

## Context

The current go2nix architecture requires a lockfile (`go2nix.toml`) with a `[pkg]` section that maps every third-party import path to its module and dependencies. For large projects this is thousands of lines (3250 entries for app-full), changes whenever imports change, and must be regenerated. The `[pkg]` section is entirely redundant — it's a cached `go list -json -deps`.

Inspired by nix-ninja and nixnative, this plan eliminates `[pkg]` by moving package graph discovery from Nix eval time to build time via recursive-nix and content-addressed (CA) derivations. The lockfile shrinks to just `[mod]` (module NAR hashes for FODs), typically 20-100 lines.

**Additionally:** local packages become individual CA derivations (like third-party), gaining the same incremental rebuild benefits. A comment-only edit that doesn't change the `.a` output won't propagate rebuilds (CA deduplication).

## Architecture

```
Current:
  go2nix generate → go2nix.toml [mod] + [pkg]
  Nix eval reads [pkg] → per-package derivations

New:
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

Fallback: existing lockfile path still works when Nix lacks dynamic-derivations
```

### Why `[mod]` must remain

Module FODs need pre-known NAR hashes. Go's `go.sum` uses tree hashes (not NAR), so we can't derive them. The `[mod]` section stays but is small and only changes when `go.mod` changes.

### Why packages are NOT built inside the wrapper

Unlike nix-ninja (which builds inside the wrapper because C/C++ has dynamic header dependencies discovered at compile time via `-MD`), Go's import graph is fully known after `go list`. We only need `nix derivation add` to register package derivations in the Nix store — Nix builds them externally when resolving `builtins.outputOf`. This gives Nix full control over build parallelism across all available cores.

The only `nix build` calls inside the wrapper are for module FODs, which must be materialized so `go list` can read their source files.

## Implementation

### Phase 1: `pkg/nixdrv/` — Nix derivation library in Go

Go equivalent of nix-ninja's `nix-libstore` + `nix-tool` crates.

| File | Purpose |
|------|---------|
| `go/go2nix/pkg/nixdrv/storepath.go` | `StorePath` type — validates `/nix/store/<32-char-hash>-<name>` format, exposes `HashPart()`, `Name()` |
| `go/go2nix/pkg/nixdrv/derivation.go` | `Derivation` struct matching `nix derivation add` JSON format — builder pattern: `NewDerivation()`, `.AddArg()`, `.SetEnv()`, `.AddCAOutput()`, `.AddInputDrv()`, `.ToJSON()` with sorted keys |
| `go/go2nix/pkg/nixdrv/placeholder.go` | SHA256-based placeholder generation — must match nix-ninja's algorithm exactly |
| `go/go2nix/pkg/nixdrv/tool.go` | `NixTool` — wraps `nix derivation add` (pipe JSON to stdin), `nix build --print-out-paths`, `nix store add` |

**Key dependency:** `github.com/nix-community/go-nix` (already in go.mod) for NAR hashing. Check if it exports nix-base32 encoding; if not, implement it (~30 lines, alphabet `0123456789abcdfghijklmnpqrsvwxyz`, reversed byte order).

#### Derivation JSON format

Matching `nix derivation add` input. nix-ninja's `Derivation` struct (from `nix-libstore/src/derivation.rs`):

```json
{
  "name": "gopkg-golang-org-x-crypto-ssh",
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "<build-script>"],
  "env": {
    "GOPROXY": "off",
    "HOME": "/homeless-shelter",
    "out": "/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9"
  },
  "inputDrvs": {
    "/nix/store/...-chacha20.drv": {
      "outputs": ["out"],
      "dynamicOutputs": {}
    }
  },
  "inputSrcs": ["/nix/store/.../go2nix"],
  "outputs": {
    "out": { "hashAlgo": "sha256", "method": "nar" }
  }
}
```

Key fields for different derivation types:

- **CA output (nar):** `"out": {"hashAlgo": "sha256", "method": "nar"}` — for package compilations and link
- **FOD:** `"out": {"hashAlgo": "sha256", "method": "nar", "hash": "<sri-hash>"}` — for module fetches (the `hash` field makes it fixed-output, Nix allows network access)

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

Derivation names from Go import paths. Match existing `helpers.nix` `sanitizeName`: replace `/` → `-`, `+` → `_`. Also replace `@` → `-`, `.` → `-`.

Examples:

- `golang.org/x/crypto/ssh` → `gopkg-golang-org-x-crypto-ssh`
- `github.com/foo/bar@v1.2.3` → `gomod-github-com-foo-bar-v1-2-3`

Prefix `gopkg-` for package derivations, `gomod-` for module FODs, `golink-` for link derivations, `gocollect-` for collector derivations.

#### Module path escaping

Go's GOMODCACHE uses case-escaped paths: uppercase letters become `!` + lowercase. This matches `golang.org/x/mod/module.EscapePath()` and the existing `helpers.nix` `escapeModPath`.

The `resolve` command must implement this in Go for constructing GOMODCACHE paths and finding source within FODs.

### Phase 2: `pkg/resolve/` — The resolve orchestrator

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
  "name": "gomod-golang-org-x-crypto-v0-17-0",
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "export HOME=$TMPDIR\nexport GOMODCACHE=$out\nexport GOSUMDB=off\nexport GONOSUMCHECK='*'\n/nix/store/.../bin/go mod download \"golang.org/x/crypto@v0.17.0\""],
  "env": {
    "out": "<standard-placeholder>"
  },
  "inputSrcs": ["/nix/store/.../go", "/nix/store/.../cacert"],
  "outputs": {
    "out": {
      "hashAlgo": "sha256",
      "method": "nar",
      "hash": "sha256-abc..."
    }
  }
}
```

The `hash` field makes this a FOD — Nix allows network access. The builder uses `GOMODCACHE=$out` so `go mod download` writes directly to the output. The NAR hash must match the lockfile entry (same computation as `modCacheHash` in `generate.go`).

For replaced modules, use `fetchPath` from `lock.Replace` instead of the module path.

**Step 3 — Materialize modules:**

```go
// Build all FODs in parallel (they're independent, no ordering constraints)
var g errgroup.Group
fodPaths := sync.Map{} // modKey → StorePath
for modKey, drvPath := range fodDrvs {
    g.Go(func() error {
        paths, err := nix.Build(drvPath.String() + "^out")
        if err != nil { return fmt.Errorf("building FOD for %s: %w", modKey, err) }
        fodPaths.Store(modKey, paths[0])
        return nil
    })
}
if err := g.Wait(); err != nil { return err }
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
  "name": "gopkg-golang-org-x-crypto-ssh",
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "<compile-script>"],
  "env": {
    "importPath": "golang.org/x/crypto/ssh",
    "goFiles": "channel.go cipher.go client.go ...",
    "modSrc": "/nix/store/...-gomod-golang-org-x-crypto-v0-17-0",
    "relDir": "golang.org/x/crypto@v0.17.0/ssh",
    "importcfg_entries": "packagefile crypto/aes=/nix/store/.../std/crypto/aes.a\npackagefile golang.org/x/crypto/curve25519=<ca-placeholder-for-curve25519>",
    "out": "<ca-placeholder>"
  },
  "inputDrvs": {
    "/nix/store/...-gopkg-curve25519.drv": {"outputs": ["out"], "dynamicOutputs": {}},
    "/nix/store/...-gopkg-internal-poly1305.drv": {"outputs": ["out"], "dynamicOutputs": {}}
  },
  "inputSrcs": [
    "/nix/store/...-gomod-golang-org-x-crypto-v0-17-0",
    "/nix/store/.../go",
    "/nix/store/.../go2nix",
    "/nix/store/.../stdlib"
  ],
  "outputs": {
    "out": {"hashAlgo": "sha256", "method": "nar"}
  }
}
```

The `relDir` is computed as `${escapeModPath(fetchPath)}@${version}/${subdir}`, matching how `process-lockfile.nix` computes `dirSuffix` + subdir.

The compile script:

```bash
set -euo pipefail
mkdir -p $out

# Write importcfg from env var (placeholders resolved by Nix at build time)
printf '%s\n' "$importcfg_entries" > importcfg

# Source files live inside the FOD's GOMODCACHE layout
srcdir="$modSrc/$relDir"

# Compile
go2nix compile-package --import-path "$importPath" \
  --importcfg importcfg --src "$srcdir" --out "$out/pkg.a" \
  --go-files "$goFiles" --trimpath "$trimpath"
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
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "<link-script>"],
  "env": {
    "mainPkg": "<ca-placeholder-for-main-pkg>",
    "importcfg_entries": "packagefile main=<ca-placeholder-for-main>/pkg.a\npackagefile github.com/foo/bar=<ca-placeholder-for-bar>/pkg.a\n...",
    "ldflags": "-s -w -X main.version=1.0",
    "out": "<ca-placeholder>"
  },
  "inputDrvs": {
    "/nix/store/...-gopkg-main.drv": {"outputs": ["out"], "dynamicOutputs": {}},
    "/nix/store/...-gopkg-dep1.drv": {"outputs": ["out"], "dynamicOutputs": {}},
    "...all transitive deps...": {"outputs": ["out"], "dynamicOutputs": {}}
  },
  "inputSrcs": ["/nix/store/.../go"],
  "outputs": {
    "out": {"hashAlgo": "sha256", "method": "nar"}
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
  "system": "x86_64-linux",
  "builder": "/nix/store/.../bin/bash",
  "args": ["-c", "mkdir -p $out/bin\ncp <link1-placeholder>/bin/* $out/bin/\ncp <link2-placeholder>/bin/* $out/bin/"],
  "inputDrvs": {
    "/nix/store/...-golink-cmd1.drv": {"outputs": ["out"], "dynamicOutputs": {}},
    "/nix/store/...-golink-cmd2.drv": {"outputs": ["out"], "dynamicOutputs": {}}
  },
  "outputs": {
    "out": {"hashAlgo": "sha256", "method": "nar"}
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

#### Parallelism

Inside the wrapper, use goroutines:

- **Module FOD creation + build**: All independent → parallel goroutines with `errgroup`
- **Package derivation creation**: Only `nix derivation add` (fast, no build). Must respect topo order for `.drv` path references, but packages at the same topo level can be created in parallel.
- **`nix build` serialization**: FOD builds should use a mutex to prevent log interleaving (nix-ninja pattern: `nix_build_lock`)

Package compilation parallelism is handled by Nix externally — it builds the full DAG with its own job scheduler (`--max-jobs`). This is more efficient than building inside the wrapper.

#### Stdlib handling

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

#### Error handling

Following nix-ninja's pattern:

- On `nix derivation add` failure: include stderr + full derivation JSON in error message
- On `nix build` failure: include the `.drv` path and stderr
- On `go list` failure: include stderr (often contains compilation errors)
- All errors are wrapped with context: `fmt.Errorf("creating FOD for %s: %w", modKey, err)`

**Shared code extraction:** Create `pkg/golist/golist.go` with `ListDeps(dir string, includeLocal bool)` and `ExtractModules()`, imported by both `generate.go` and `resolve.go`.

### Phase 3: CLI + Nix wrapper

**Go:**

- `go/go2nix/cmd/go2nix/resolve.go` — new subcommand with flags: `--src`, `--lockfile`, `--system`, `--go`, `--stdlib`, `--nix`, `--pname`, `--sub-packages`, `--tags`, `--ldflags`, `--overrides`, `--output`
- `go/go2nix/cmd/go2nix/main.go` — add `case "resolve":` to switch

**Nix — `nix/build-go-application-dynamic.nix`:**

```nix
{ lib, stdenv, go, go2nix, nixPackage, coreutils, bash, cacert, buildGoStdlib }:

{ pname
, src
, goLock
, subPackages ? [ "." ]
, tags ? []
, ldflags ? []
, packageOverrides ? {}  # for cgo: { "import/path" = { nativeBuildInputs = [...]; }; }
, ...
}:

let
  stdlib = buildGoStdlib { inherit go; };

  # Serialize packageOverrides to JSON for the resolve command
  overridesJSON = builtins.toJSON (lib.mapAttrs (path: cfg: {
    nativeBuildInputs = map toString (cfg.nativeBuildInputs or []);
  }) packageOverrides);

  wrapperDrv = stdenv.mkDerivation {
    name = "${pname}.drv";

    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";
    requiredSystemFeatures = [ "recursive-nix" ];

    # Prevent self-references in text-mode output (stdenv adds -rpath with self ref)
    NIX_NO_SELF_RPATH = true;

    nativeBuildInputs = [
      go go2nix nixPackage coreutils bash cacert
    ] ++ lib.concatMap (cfg: cfg.nativeBuildInputs or [])
         (lib.attrValues packageOverrides);

    buildPhase = ''
      export NIX_CONFIG="extra-experimental-features = nix-command ca-derivations dynamic-derivations"
      go2nix resolve \
        --src ${src} \
        --lockfile ${goLock} \
        --system ${stdenv.hostPlatform.system} \
        --go ${go}/bin/go \
        --stdlib ${stdlib} \
        --nix ${nixPackage}/bin/nix \
        --pname ${pname} \
        --sub-packages ${lib.escapeShellArg (lib.concatStringsSep "," subPackages)} \
        --tags ${lib.escapeShellArg (lib.concatStringsSep "," tags)} \
        --ldflags ${lib.escapeShellArg (lib.concatStringsSep " " ldflags)} \
        --overrides ${lib.escapeShellArg overridesJSON} \
        --cacert ${cacert}/etc/ssl/certs/ca-bundle.crt \
        --output $out
    '';

    passthru = {
      target = builtins.outputOf wrapperDrv.outPath "out";
    };
  };

in wrapperDrv
```

**How `builtins.outputOf` works (from Nix source `src/libexpr/primops.cc`):**

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

**Nix — scope updates:**

- `nix/scope.nix` — add `buildGoApplicationDynamic`, accept optional `nixPackage` param
- `nix/mk-go-env.nix` — plumb `nixPackage`

### Phase 4: Lockfile simplification

- `generate.go` — `--mode=dynamic` skips `[pkg]` collection
- `lockfile.go` — add `omitempty` to `Pkg` field so TOML omits it when nil
- `resolve` reads only `Mod` + `Replace`, ignores `Pkg`

### Phase 5: Feature detection + fallback

```nix
hasDynamicDerivations = builtins ? outputOf;

buildGoApplication =
  if hasDynamicDerivations && nixPackage != null
  then buildGoApplicationDynamic
  else buildGoApplicationLockfile;  # existing path
```

## Cgo handling

Packages needing extra `nativeBuildInputs` (pkg-config, libfoo) are currently handled by `packageOverrides` at eval time. For dynamic mode:

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

## Files summary

| File | Change |
|------|--------|
| `go/go2nix/pkg/nixdrv/storepath.go` | **New** — StorePath validation |
| `go/go2nix/pkg/nixdrv/derivation.go` | **New** — Derivation JSON struct |
| `go/go2nix/pkg/nixdrv/placeholder.go` | **New** — Placeholder generation |
| `go/go2nix/pkg/nixdrv/tool.go` | **New** — Nix CLI wrapper |
| `go/go2nix/pkg/golist/golist.go` | **New** — Extracted go list wrapper |
| `go/go2nix/pkg/resolve/resolve.go` | **New** — Resolve orchestrator |
| `go/go2nix/pkg/resolve/graph.go` | **New** — Package graph types |
| `go/go2nix/pkg/resolve/builder.go` | **New** — Builder script generation |
| `go/go2nix/cmd/go2nix/resolve.go` | **New** — CLI subcommand |
| `go/go2nix/cmd/go2nix/main.go` | Add `resolve` case |
| `nix/build-go-application-dynamic.nix` | **New** — Recursive-nix wrapper |
| `nix/scope.nix` | Add dynamic builder + nixPackage |
| `nix/mk-go-env.nix` | Plumb nixPackage param |
| `go/go2nix/pkg/lockfilegen/generate.go` | `--mode` flag (dag/dynamic/vendor) |
| `go/go2nix/pkg/lockfile/lockfile.go` | Add `omitempty` to Pkg |

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

## Verification

1. **Phase 1:** Unit tests for derivation JSON serialization, placeholder test vectors matching nix-ninja (3 vectors above), store path validation, nix-base32 encoding, module path escaping
2. **Phase 2:** Integration test with yubikey-agent — `go2nix resolve` produces binary (requires Nix with `recursive-nix ca-derivations dynamic-derivations`)
3. **Phase 3:** `nix build` of test packages using `buildGoApplicationDynamic`
4. **Phase 4:** `go2nix generate --mode=dynamic` produces lockfile with only `[mod]`, `resolve` works with it
5. **Phase 5:** Fallback to lockfile path when dynamic features unavailable
6. **Rebuild test:** Edit a comment in a dependency, verify CA derivation cache hit (no recompilation of unchanged packages)
