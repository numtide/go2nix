# go2nix builder

Nix functions (`buildGoApplication`, `mkGoEnv`) that consume a composite-key
lockfile (`go2nix.toml`) to produce vendored Go builds.

## Attribution

Derived from [`nix-community/gomod2nix/builder`] at
[`47d628dc`](https://github.com/nix-community/gomod2nix/commit/47d628dc3b506bd28632e47280c6b89d3496909d)
(Aug 2025). MIT licensed — see [`LICENSE`](LICENSE).

[`nix-community/gomod2nix/builder`]: https://github.com/nix-community/gomod2nix/tree/master/builder

## Changes from upstream

### Added

- **Composite `module@version` keys** ([`default.nix`](default.nix) `mkVendorEnv`):
  the lockfile is filtered at eval time against the project's `go.mod` `require`
  directive. One lockfile serves N projects; a missing or stale entry is a build
  failure, not a silent wrong-dependency. This is the core idea.

- **`netrcFile`** parameter ([`default.nix`](default.nix), [`fetch.sh`](fetch.sh)):
  threads netrc contents through to `go mod download` for authenticated private
  modules. Same approach as upstream [PR #243].

- **`goModFile`** parameter ([`default.nix`](default.nix)): lets the caller pass
  a raw path to `go.mod` instead of having it read from `pwd`, avoiding
  Import-From-Derivation when `pwd` is itself a derivation output. Same approach
  as upstream [PR #243].

- **`parser.nix` fix** for `go.mod` files mixing single-line and parenthesized
  `require` blocks: upstream's fold-left overwrites the accumulator; we merge.
  Standalone bugfix, no dependency on the other changes.

[PR #243]: https://github.com/nix-community/gomod2nix/pull/243

### Removed

These are upstream features we don't use. Not a value judgment — they'd work
fine here, they're just orthogonal to the composite-key change.

- **`hooks/`** (`goConfigHook`/`goBuildHook`/`goCheckHook`/`goInstallHook`):
  upstream moved build phases to `makeSetupHook` after we forked. We still have
  the older inline `buildPhase`/`checkPhase`/`installPhase` shell heredocs.
- **`mkGoCacheEnv`** + **`cachegen/`**: build-cache pre-warming via zstd tarballs.
- **`updateScript`**: a `writeScript` wrapper around `gomod2nix generate`.
  Replaced by the `go2nix` CLI.

### Unchanged

[`symlink/symlink.go`](symlink/symlink.go) and [`install/install.go`](install/install.go)
are byte-identical to upstream `@47d628dc`.

## Files

```
default.nix          buildGoApplication, mkGoEnv, mkVendorEnv, fetchGoModule
parser.nix           go.mod -> Nix attrset (require, replace, etc.)
fetch.sh             fixed-output derivation builder: `go mod download` one module
symlink/symlink.go   build the vendor/ tree from JSON module spec
install/install.go   install tools from tools.go (used by mkGoEnv)
```
