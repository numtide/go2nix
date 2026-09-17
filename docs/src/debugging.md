# Debugging a Build

A go2nix build is many small derivations, so the first step is always the
same: find out *which* one failed, then look at that one alone. This page is
about the default builder; [Troubleshooting](troubleshooting.md) has the
known error messages.

## Which derivation failed

Nix names it:

```
error: builder for '/nix/store/…-gopkg-github.com-mattn-go-isatty-v0.0.20.drv' failed with exit code 1
```

| Name | What it was doing |
|------|-------------------|
| `gomod-<module>-<version>` | downloading one module (`go mod download`) |
| `gopkg-<import path>-<version>` | compiling one third-party package |
| `golocal-<import path>` | compiling one package of yours |
| `go-stdlib-…` | compiling the standard library |
| `<pname>-deps-importcfg`, `<pname>-test-deps-importcfg` | concatenating import maps; does not fail on its own |
| `<pname>-<version>` | compiling the main packages, linking, running the tests |

In a name `/` became `-`, `@` became `_at_` and `~` became `_`, so
`gopkg-golang.org-x-sys-unix-v0.25.0` is the package `golang.org/x/sys/unix`
of module version `v0.25.0`. `nix log <that .drv>` prints its build log, which
for a compile is the compiler's own output.

If the failure is an *evaluation* error (`error: resolveGoPackages: …`,
`attribute … missing`), nothing was built yet: go to
[Troubleshooting](troubleshooting.md).

## Build one package alone

The application derivation carries the whole graph in `passthru`, keyed by
import path:

```bash
# what is in the build
nix eval .#my-app.passthru.packages --apply builtins.attrNames
nix eval .#my-app.passthru.localPackages --apply builtins.attrNames

# build one, and only what it needs
nix build '.#my-app.passthru.packages."github.com/fatih/color"' --print-out-paths
nix build '.#my-app.passthru.localPackages."example.com/my-app/internal/db"'

# keep the build directory of a failing one
nix build '.#my-app.passthru.packages."github.com/mattn/go-sqlite3"' --keep-failed
```

`passthru` has `packages` (third-party), `localPackages`, `testPackages`
(third-party packages only tests import, with `doCheck`), `depsImportcfg`,
`testDepsImportcfg`, `mainSrc` (the filtered source the final derivation
sees), `modulePath`, `go`, `go2nix` and `goLock`.

A package's output is small and readable:

```
$ find result -type f
result/importcfg
result/github.com/fatih/color.a
$ cat result/importcfg
packagefile github.com/fatih/color=/nix/store/…-gopkg-github.com-fatih-color-v1.18.0/github.com/fatih/color.a
```

With `contentAddressed = true` a local package also has an `iface` output
holding the `.x` export data and the `importcfg` its dependents read.

## What a package derivation was told

Everything a compile gets is in its derivation, as plain environment
variables:

```bash
pkg='.#my-app.passthru.packages."github.com/fatih/color"'
nix derivation show "$pkg" \
    | jq '.. | .env? | select(.) | {goPackagePath, goPackageSrcDir, goModulePath, goModuleVersion}'
nix derivation show "$pkg" \
    | jq '.. | .compileManifestJSON? | select(.) | fromjson'
```

```json
{
  "version": 2,
  "kind": "compile",
  "files": { "goFiles": [ "color.go", "doc.go" ] },
  "importcfgParts": [ "/nix/store/…-go-stdlib-1.26.5/importcfg", "…" ],
  "tags": [],
  "gcflags": [],
  "pgoProfile": null
}
```

`goPackageSrcDir` is the directory that gets compiled: inside the module's
fetch output for a third-party package, a filtered copy of the package's own
directory for a local one. The manifest has the rest: `files` (exactly which
files `go list` selected for this platform and tag set, by kind: `goFiles`,
`cgoFiles`, `sFiles`, `cFiles`, `cxxFiles`, `hFiles`, `sysoFiles`, …, and
`embedPatterns`), `importcfgParts` (the import maps of its dependencies),
`tags`, `gcflags`, `pgoProfile`. If a file you expected is not in `files`, a
build constraint excluded it (above, `color_windows.go` on Linux), and the
question is about `tags`, `GOOS`/`GOARCH` or `CGO_ENABLED`, not about go2nix.

The application derivation has `linkManifestJSON` and, with `doCheck`,
`testManifestJSON` in the same way.

## See what `go` sees

The CLI has inspection commands that apply Go's own file and package
selection to a directory, outside any build:

```bash
nix run github:numtide/go2nix -- list-files -tags=netgo ./internal/db
nix run github:numtide/go2nix -- list-packages .
```

See the [CLI Reference](cli-reference.md#inspection-tools).

## More output

`GO2NIX_DEBUG=1` switches the CLI to debug logging. Inside a build it has to be
in the derivation's environment: `env.GO2NIX_DEBUG = "1"` on
`buildGoApplication` for the link and test steps,
`packageOverrides.<import path>.env.GO2NIX_DEBUG = "1"` for one package's
compile. Either changes the derivation, so expect it to rebuild.

## When the wrong thing rebuilds

Compare the two derivations instead of guessing:

```bash
nix derivation show '.#my-app.passthru.localPackages."example.com/my-app/internal/db"' > before.json
# make the change
nix derivation show '.#my-app.passthru.localPackages."example.com/my-app/internal/db"' > after.json
diff <(jq -S . before.json) <(jq -S . after.json)
```

An input that changed is either `goPackageSrcDir` (a file in the package's
directory changed, tests and `testdata/` included), an `importcfgParts` entry
(a dependency changed; follow it), or a flag in the manifest.
[Incremental Builds](incremental-builds.md) lists what each kind of derivation
is keyed on, and [`nix-diff`](https://github.com/Gabriella439/nix-diff) does
the same comparison recursively.
