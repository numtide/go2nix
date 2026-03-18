# DAG Mode

Per-package Nix derivations at eval time, with fine-grained caching.

## Overview

DAG mode creates one Nix derivation per third-party Go package. The package
dependency graph is discovered at eval time by the go-nix-plugin via
`builtins.resolveGoPackages`, which runs `go list` against the source tree.
Module hashes are read from the lockfile's `[mod]` section. When a single
dependency changes, only it and its reverse dependencies rebuild.

## Lockfile requirements

DAG mode requires a lockfile with `[mod]` (and optionally `[replace]`)
sections:

```bash
go2nix generate --mode=dag .
```

The lockfile contains only module hashes — the package graph is resolved at
eval time by the plugin, so the lockfile does not need to be regenerated when
import relationships change (only when modules are added or removed).

## Nix evaluation flow

### 1. Module resolution (builtins.resolveGoModules)

Parses the TOML lockfile and returns `{ modules }`:

- **modules**: Per-module metadata (path, version, hash, dirSuffix, fetchPath)

### 2. Package graph discovery (builtins.resolveGoPackages)

The go-nix-plugin runs `go list -json -deps` against the source tree at eval
time and returns `{ packages, replacements }`:

- **packages**: Per-package metadata (modKey, subdir, imports, drvName, isCgo)
- **replacements**: Module replacement mappings from `go.mod` `replace` directives

Replace directives are applied to module `fetchPath` and `dirSuffix` fields
so that FODs download from the correct path.

### 3. Module fetching (fetch-go-module.nix)

Each module is a fixed-output derivation (FOD) that downloads via `go mod download` and produces a GOMODCACHE directory layout:

```
$out/<escaped-path>@<version>/
```

The `netrcFile` option supports private module authentication.

### 4. Package derivations (default.nix)

For each package in `goPackagesResult.packages`, a derivation is created:

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

### 5. Application derivation

The final derivation takes all third-party packages as `buildInputs` and
uses `goAppHook` (`link-go-binary.sh`) to:

1. Validate lockfile consistency
1. Compile local packages
1. Link the final binary

## Package overrides

Per-package customization (e.g., for cgo libraries):

```nix
goEnv.buildGoApplicationDAGMode {
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
final link step.

## Directory layout

```
nix/dag/
├── default.nix            # buildGoApplicationDAGMode
├── fetch-go-module.nix    # FOD fetcher
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
- Small lockfile (just `[mod]` hashes, no `[pkg]` section)
- Lockfile only changes when modules are added/removed, not when imports change
- Automatic CGO detection and compiler injection

**Cons:**

- Requires the go-nix-plugin (provides `builtins.resolveGoPackages`)
- Many small derivations can slow Nix evaluation on very large projects

See [compilation-pipeline.md](../internals/compilation-pipeline.md) for details on how
compilation and linking work.
