# Default Mode

Per-package Nix derivations at eval time, with fine-grained caching.

## Overview

The default mode creates per-package derivations from an eval-time package
graph. The go2nix-nix-plugin runs `builtins.resolveGoPackages` to discover
third-party packages, local packages, local replaces, module metadata, and
optional test-only third-party packages when checks are enabled. Module hashes
are read from the lockfile's `[mod]` section, with optional `[replace]` entries
applied to module fetch paths. When a single dependency changes, only it and
its reverse dependencies rebuild.

## Lockfile requirements

The default mode requires a lockfile with `[mod]` (and optionally `[replace]`)
sections:

```bash
go2nix generate .
```

The lockfile contains only module hashes — the package graph is resolved at
eval time by the plugin, so the lockfile does not need to be regenerated when
import relationships change (only when modules are added or removed).

## Nix evaluation flow

### 1. Module resolution (builtins.resolveGoModules)

Parses the TOML lockfile and returns `{ modules }`:

- **modules**: Per-module metadata (path, version, hash, dirSuffix, fetchPath)

### 2. Package graph discovery (builtins.resolveGoPackages)

The go2nix-nix-plugin runs `go list -json -deps` against the source tree at eval
time and returns a package graph:

- **packages**: Third-party package metadata (modKey, subdir, imports, drvName, isCgo)
- **localPackages**: Local package metadata (dir, localImports, thirdPartyImports, isCgo)
- **modulePath**: Main module import path
- **replacements**: Module replacement mappings from `go.mod` `replace` directives
- **localReplaces**: Filesystem replace directives for local modules
- **testPackages**: Test-only third-party package metadata when `doCheck = true`

Replace directives are applied to module `fetchPath` and `dirSuffix` fields
so that FODs download from the correct path.

### 3. Module fetching (fetch-go-module.nix)

Each module is a fixed-output derivation (FOD) that downloads via `go mod download` and produces a GOMODCACHE directory layout:

```
$out/<escaped-path>@<version>/
```

The `netrcFile` option supports private module authentication.

### 4. Package derivations (default.nix)

For each third-party package in `goPackagesResult.packages`, a derivation is
created:

```nix
stdenv.mkDerivation {
  name = pkg.drvName;                         # "gopkg-github.com-foo-bar"
  nativeBuildInputs = [ hooks.goModuleHook ]  # compile-go-pkg.sh
    ++ cgoBuildInputs;                        # stdenv.cc for CGO packages
  buildInputs = deps;                         # dependency package derivations
  env = {
    goPackagePath = importPath;
    goPackageSrcDir = srcDir;
  };
}
```

CGO packages (where `pkg.isCgo` is true) automatically get `stdenv.cc` added
to `nativeBuildInputs`.

Dependencies (`deps`) are resolved lazily via Nix's laziness — each package
references other packages from the same `packages` attrset.

### 5. Local package derivations

Each local package in `goPackagesResult.localPackages` also gets its own
derivation with a source tree filtered down to that package directory plus its
parents. Local package dependencies can point to other local packages and to
third-party packages.

### 6. Importcfg bundles

Instead of passing every compiled package as a direct dependency of the final
application derivation, the default mode builds bundled `importcfg` derivations:

- `depsImportcfg`: stdlib + third-party + local packages
- `testDepsImportcfg`: adds test-only third-party packages when `doCheck = true`

This keeps the final derivation's input fan-in small while preserving
fine-grained package caching.

### 7. Application derivation

The final derivation consumes `depsImportcfg` (and `testDepsImportcfg` when
checks are enabled) and uses `goAppHook` to:

1. Validate lockfile consistency
1. Link the final binary
1. Run tests when `doCheck = true`

## Package overrides

Per-package customization (e.g., for cgo libraries):

```nix
goEnv.buildGoApplication {
  src = ./.;
  goLock = ./go2nix.toml;
  pname = "my-app";
  version = "0.1.0";
  tags = [ "netgo" ];
  packageOverrides = {
    "github.com/mattn/go-sqlite3" = {
      nativeBuildInputs = [ pkg-config sqlite ];
    };
  };
}
```

Overrides apply to both the per-package derivation and are collected for the
final application derivation.

## Directory layout

```
nix/dag/
├── default.nix            # buildGoApplication
├── fetch-go-module.nix    # FOD fetcher
└── hooks/
    ├── default.nix        # Hook definitions
    ├── setup-go-env.sh    # GOPROXY=off, GOSUMDB=off
    ├── compile-go-pkg.sh  # Compile one package
    └── link-go-binary.sh  # Link binary and run checks
```

## Trade-offs

**Pros:**

- Fine-grained caching — changing one dependency doesn't rebuild everything
- No experimental Nix features required
- Small lockfile (`[mod]` plus optional `[replace]`, no `[pkg]` section)
- Lockfile only changes when modules are added/removed, not when imports change
- Automatic CGO detection and compiler injection

**Cons:**

- Requires the go2nix-nix-plugin (provides `builtins.resolveGoPackages`)
- Many small derivations can slow Nix evaluation on very large projects

Compilation and linking are handled by the builder hooks and direct
`go tool compile` / `go tool link` invocations described above.
