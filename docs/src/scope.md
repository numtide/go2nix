# The Scope

`go2nix.lib.mkGoEnv` returns a *scope*: a package set made with
`lib.makeScope` in which everything shares one Go toolchain, one go2nix CLI
and one compiled standard library. Most projects only ever call
`buildGoApplication` on it. The rest is what the builders are made of, and it
is there to be read, reused and overridden. The arguments of `mkGoEnv` itself
are in the [Builder API](builder-api.md#mkgoenv).

## Members

| Member | What it is |
|--------|------------|
| `buildGoApplication` | The default builder. See [Builder API](builder-api.md). |
| `buildGoApplicationExperimental` | The experimental builder; throws unless `mkGoEnv` was given `nixPackage`. See [Experimental Mode](modes/experimental-mode.md). |
| `go`, `go2nix`, `nixPackage`, `netrcFile` | What `mkGoEnv` was given. |
| `goEnv` | What `mkGoEnv` was given, with `GOOS` and `GOARCH` filled in from the host platform when cross-compiling. |
| `stdlib` | The compiled standard library; [below](#stdlib). |
| `fetchers.fetchGoModule` | The function that makes a module's fetch derivation; [below](#fetchersfetchgomodule). |
| `hooks` | Setup hooks used by the derivations that need stdenv; [below](#hooks). |
| `helpers` | Pure functions shared by the builders; [below](#helpers). |
| `tags`, `tagFlag` | The `tags` given to `mkGoEnv` and their comma-joined form. Neither builder reads them; pass `tags` to each build. |
| `hasDynamicDerivations` | `builtins ? outputOf`: whether this Nix can evaluate the experimental builder's result. |
| `callPackage`, `newScope`, `overrideScope`, `packages`, `lib` | From `lib.makeScope` and nixpkgs. |

## Overriding

`overrideScope` takes an overlay and returns a new scope in which every
member, the builders included, sees the change:

```nix
goEnv' = goEnv.overrideScope (final: prev: {
  fetchers = prev.fetchers // {
    fetchGoModule = args:
      (prev.fetchers.fetchGoModule args).overrideAttrs (old: {
        impureEnvVars = old.impureEnvVars ++ [ "GOPRIVATE" ];
      });
  };
});
```

`goEnv'.buildGoApplication` now fetches every module through the wrapped
function. Because a fetch is a fixed-output derivation, changing how it
fetches does not change its output path.

Two scopes with a different `go` or `goEnv` have different standard
libraries, and therefore share no compiled package; two applications built
from the *same* scope share every third-party package they have in common.

## `stdlib`

One derivation per scope, named `go-stdlib-<go version>`, plus `-<8 hex>`
(a hash of `goEnv`) when `goEnv` is not empty. It copies `GOROOT` and runs
`go install --trimpath std` with `goEnv` exported, so settings that change
which standard-library sources get compiled (`GOFIPS140`, `GOEXPERIMENT`,
`CGO_ENABLED`, `GOOS`/`GOARCH`) belong in `goEnv`. Output:

- `$out/<import path>.a` for every standard-library package
- `$out/importcfg`, one `packagefile <import path>=<archive>` line each

Every compile reads that `importcfg`; this is the only place go2nix runs
`cmd/go`. Packages and the final link use `go tool compile` and
`go tool link` directly.

## `fetchers.fetchGoModule`

```nix
goEnv.fetchers.fetchGoModule {
  fetchPath = "github.com/fatih/color";   # where to download from
  version = "v1.18.0";
  hash = "sha256-pP5y72FSbi4j/BjyVq/XbAOFjzNjMxZt2R/lFFxGWvY=";
  goProxy = null;                          # optional
}
```

Returns a fixed-output derivation named `gomod-<fetchPath>-<version>` (with
`/` turned into `-`) that runs `go mod download <fetchPath>@<version>` and
keeps the module's extracted source tree as `$out`: what `go` would put in
`$GOMODCACHE/<path>@<version>/`, with the download metadata left out. That
makes the hash the same whichever proxy served the module, and the same one
`go2nix generate` and the lockfile-free resolver compute, so all three agree
on one derivation per module version.

For a replaced module, `fetchPath` and `version` are the replacement's; the
builder takes care of that.

How the fetch reaches the network:

- `GOPROXY` and `NETRC` are inherited from the environment of whatever runs
  the build (`impureEnvVars`), along with the usual proxy variables.
  `goProxy` exports `GOPROXY` inside the derivation instead, which is what
  you want under a daemon or a remote builder, where that environment is
  not yours.
- The scope's `netrcFile` is copied to `$HOME/.netrc`.
- `GOSUMDB=off`: the fixed output hash is the integrity check.
- Only `go` and CA certificates are on `PATH`. There is no `git`, so a module
  that `direct` would fetch from version control cannot be fetched that way;
  it has to come from a proxy.

## `hooks`

Pure-Go packages are compiled by a bare `builtins.derivation` that calls
`go2nix compile-package` directly. The hooks are for the derivations that
need stdenv: cgo packages (for the C compiler) and the final application.

| Hook | Used by | Reads |
|------|---------|-------|
| `setupGoEnv` | both hooks below | sets `HOME`, `GOPROXY=off`, `GOSUMDB=off`, `GONOSUMCHECK=*` before configure |
| `goModuleHook` | cgo package derivations | `goPackagePath`, `goPackageSrcDir`, `goLangVersion`, `goModulePath`, `goModuleVersion`, `compileManifestJSON`, optionally `goSrcOverlay`; writes the `iface` output when the derivation has one |
| `goAppHook` | the application derivation | `linkManifestJSON`, and `testManifestJSON` when `doCheck` is on; installs `$out/bin/*` |

## `helpers`

| Function | Contract |
|----------|----------|
| `sanitizeName s` | `s` made safe for a derivation name: `/` becomes `-`, `~` becomes `_`, `@` becomes `_at_`; past 160 characters the tail is replaced by `-` and 8 hex digits of `sha256 s`. The Go CLI and the plugin implement the same rule, because all three must produce the same names. |
| `escapeModPath s` | Go's module path escaping: every upper-case letter becomes `!` and its lower-case form, the layout of `GOMODCACHE`. |
| `normalizeSubPackages list` | Prefixes `./` to every entry that is not `.` and does not already start with `./`, so that `cmd/foo` is never taken for a standard-library import path. |
| `parseLocalReplaces text` | The filesystem targets (`./…`, `../…`) of the `replace` directives in the text of a `go.mod`. |
| `goModLocalReplaceDirs dir` | `dir` plus every directory reachable from its `go.mod` through filesystem `replace` directives, transitively. |
| `removePrefix prefix s` | `s` without its first `stringLength prefix` characters. |

`goModLocalReplaceDirs` is the useful one outside the builders: it answers
"which directories does this module need?" for a module that lives in a
larger repository, so `src` can be just those:

```nix
src = lib.fileset.toSource {
  root = ./.;
  fileset = lib.fileset.unions (goEnv.helpers.goModLocalReplaceDirs ./services/api);
};
modRoot = "services/api";
```
