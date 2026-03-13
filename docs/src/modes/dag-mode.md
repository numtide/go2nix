# DAG Mode

Per-package Nix derivations at eval time, with fine-grained caching.

## Overview

DAG mode creates one Nix derivation per third-party Go package. The package
dependency graph is recorded in the lockfile's `[pkg]` section at generation
time, and the Nix evaluator creates derivations at eval time. When a single
dependency changes, only it and its reverse dependencies rebuild.

## Lockfile requirements

DAG mode requires a full lockfile with both `[mod]` and `[pkg]` sections:

```bash
go2nix generate --mode=dag .
```

The `[pkg]` section encodes the import graph so Nix can wire up `buildInputs`
between package derivations without running Go at eval time.

## Nix evaluation flow

### 1. Lockfile processing (process-lockfile.nix)

Parses the TOML lockfile and returns `{ modules, packages }`:

- **modules**: Per-module metadata (path, version, hash, dirSuffix, fetchPath)
- **packages**: Per-package metadata (modKey, subdir, imports, drvName)

A WASM fast path (`go2nix.wasm`) is used when `builtins.wasm` is available,
with a pure-Nix fallback otherwise.

### 2. Module fetching (fetch-go-module.nix)

Each module is a fixed-output derivation (FOD) that downloads via `go mod
download` and produces a GOMODCACHE directory layout:

```
$out/<escaped-path>@<version>/
```

The `netrcFile` option supports private module authentication.

### 3. Package derivations (default.nix)

For each package in `processed.packages`, a derivation is created:

```nix
stdenv.mkDerivation {
  name = pkg.drvName;                         # "gopkg-github.com-foo-bar"
  nativeBuildInputs = [ hooks.goModuleHook ]; # compile-go-pkg.sh
  buildInputs = deps;                         # dependency package derivations
  env = {
    goPackagePath = importPath;
    goPackageSrcDir = srcDir;
  };
}
```

Dependencies (`deps`) are resolved lazily via Nix's laziness — each package
references other packages from the same `packages` attrset.

### 4. Application derivation

The final derivation takes all third-party packages as `buildInputs` and
uses `goAppHook` (`link-go-binary.sh`) to:

1. Validate lockfile consistency
2. Compile local packages
3. Link the final binary

## Package overrides

Per-package customization (e.g., for cgo libraries):

```nix
goEnv.buildGoApplicationDAGMode {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  packageOverrides = {
    "github.com/mattn/go-sqlite3" = {
      nativeBuildInputs = [ pkg-config sqlite ];
    };
  };
}
```

Overrides apply to both the per-package derivation and are collected for the
final link step.

## Directory layout

```
nix/dag/
├── default.nix            # buildGoApplicationDAGMode
├── fetch-go-module.nix    # FOD fetcher
├── process-lockfile.nix   # TOML → {modules, packages}
├── go2nix.wasm            # Fast TOML parser (optional)
└── hooks/
    ├── default.nix        # Hook definitions
    ├── setup-go-env.sh    # GOPROXY=off, GOSUMDB=off
    ├── compile-go-pkg.sh  # Compile one third-party package
    └── link-go-binary.sh  # Compile local pkgs + link binary
```

## Trade-offs

**Pros:**
- Fine-grained caching — changing one dependency doesn't rebuild everything
- No experimental Nix features required
- Full dependency graph visible at eval time

**Cons:**
- Larger lockfile (includes `[pkg]` section with all import relationships)
- Lockfile must be regenerated when import graph changes
- Many small derivations can slow Nix evaluation on very large projects

See [compilation-pipeline.md](../internals/compilation-pipeline.md) for details on how
compilation and linking work.
