# Migrating

How a `buildGoModule` or gomod2nix expression translates, attribute by
attribute, and what behaves differently afterwards. The `buildGoModule` side
is as of nixpkgs' `pkgs/build-support/go/module.nix`.

## From `buildGoModule`

```nix
# before
pkgs.buildGoModule {
  pname = "my-app";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-…";
  subPackages = [ "cmd/server" ];
  ldflags = [ "-s" "-w" "-X main.version=0.1.0" ];
  tags = [ "netgo" ];
  env.CGO_ENABLED = 0;
}

# after
goEnv.buildGoApplication {
  pname = "my-app";
  version = "0.1.0";
  src = ./.;
  goLock = ./go2nix.toml;            # generated; or null
  subPackages = [ "cmd/server" ];
  ldflags = [ "-s" "-w" "-X main.version=0.1.0" ];
  tags = [ "netgo" ];
}
```

with, once per flake,

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  goEnv.CGO_ENABLED = "0";           # the standard library is compiled with it too
};
```

and the [plugin](nix-plugin.md) loaded. See
[Getting Started](getting-started.md) for both.

| `buildGoModule` | go2nix | Notes |
|-----------------|--------|-------|
| `pname`, `version`, `src`, `meta`, `passthru` | the same | |
| `vendorHash` | `goLock = ./go2nix.toml`, or `goLock = null` | One hash per module instead of one over the whole vendor tree; `go2nix generate .` writes the file. Bumping one module re-fetches one module. |
| `proxyVendor`, `deleteVendor`, `goSum`, `overrideModAttrs` | none needed | There is no vendor step. Every module is its own fetch derivation, made with `go mod download`. A `vendor/` directory in `src` is ignored. To change how modules are fetched, override `fetchers.fetchGoModule` in the [scope](scope.md#overriding). |
| `modRoot` | `modRoot` | Same meaning. Default `"."`. |
| `subPackages` | `subPackages` | **The default differs.** `buildGoModule` builds every directory that has Go files when `subPackages` is not set; go2nix builds `[ "." ]`, the package at `modRoot`. List your main packages. |
| `excludedPackages` | none | Say what to build with `subPackages` instead. |
| `ldflags` | `ldflags` | Passed to `go tool link`. |
| `tags` | `tags` | Per build, not on `mkGoEnv`. |
| `GOFLAGS` | none | Nothing here runs `go build`, so there is no `GOFLAGS` to read. `-trimpath` is always on; compiler flags go in `gcflags`. |
| `env.CGO_ENABLED` | `CGO_ENABLED`, or `goEnv.CGO_ENABLED` on the scope | The scope's value also decides how the standard library is compiled. |
| other `env.*` | `env` for the final derivation; `goEnv` on the scope for what the toolchain must see (`GOEXPERIMENT`, `GOFIPS140`, `GOOS`, `GOARCH`) | |
| `nativeBuildInputs`, `buildInputs` (for cgo) | `packageOverrides.<import path>.nativeBuildInputs` | The C compiler runs in the derivation of the package that has the cgo code, so that is where `pkg-config` and the library go. See [Package Overrides](package-overrides.md) and [how to find the package](recipes.md#cgo-packages-that-need-a-system-library). Top-level inputs reach the final link only. |
| `doCheck` | `doCheck` | Both default to `true`. go2nix tests the local packages that are part of the build, not every directory with a `_test.go`. |
| `checkFlags = [ "-v" "-run" "X" ]` | `checkFlags = [ "-test.v" "-test.run" "X" ]` | The flags go to the test binary itself, not through `go test`. |
| `buildTestBinaries` | none | |
| `allowGoReference` | `allowGoReference` | Same meaning and default. |
| `enableParallelBuilding` | none | Parallelism is Nix building independent package derivations. |
| `preBuild`, `postInstall`, other phases and hooks | passed through | They run in the final derivation, which compiles the main packages, links and tests. They cannot affect how a dependency or a library package of yours is compiled: that happened in another derivation. |
| `go generate` in `preBuild` | `packageOverrides.<import path>.srcOverlay`, or commit the generated files | A package is compiled from its directory in `src`, plus an optional overlay derivation laid over it. |
| `buildGoModule.override { go = …; }` | `mkGoEnv { go = …; }` | The toolchain is a property of the scope. |
| `pkgsCross.….buildGoModule` | `pkgsCross.….callPackage` into `mkGoEnv`, or `goEnv.GOOS`/`GOARCH` | See [Cross-compilation](builder-api.md#cross-compilation). |

### What changes

- **One derivation becomes many.** `nix build` output, `nix log`, and
  `nix why-depends` now talk about `gopkg-…` and `golocal-…` derivations. A
  compile error is in the log of the package that failed; see
  [Debugging a build](debugging.md).
- **Evaluation does work.** The plugin runs `go list` every time Nix
  evaluates the expression, so the modules must be in `GOMODCACHE` or
  downloadable there, and evaluation is slower than reading a hash. In
  exchange a change rebuilds only what depends on it.
- **The evaluator needs the plugin**, on every machine that evaluates: yours,
  CI, anything that runs `nix flake check`. Machines that only *build* (remote
  builders, the daemon) need nothing.
- **Tests run from a read-only copy of the sources**, one package after the
  other, with no `go vet` pass. See [Test Support](test-support.md).
- **`vendorHash = null`** (dependencies vendored in the tree) has no
  equivalent: go2nix fetches modules itself.

If none of this buys you anything, because the project is small and rebuilds
are rare, `buildGoModule` is the simpler tool.

## From gomod2nix

```nix
# before
gomod2nix.buildGoApplication {
  pname = "my-app";
  version = "0.1.0";
  pwd = ./.;
  src = ./.;
  modules = ./gomod2nix.toml;
}

# after
goEnv.buildGoApplication {
  pname = "my-app";
  version = "0.1.0";
  src = ./.;
  goLock = ./go2nix.toml;
}
```

| gomod2nix | go2nix | Notes |
|-----------|--------|-------|
| `gomod2nix.toml`, from `gomod2nix generate` | `go2nix.toml`, from `go2nix generate .` | Different files and formats; both hold one hash per module. go2nix's keys are `"path@version"` and carry no package information. Delete the old file once nothing reads it. |
| `modules = ./gomod2nix.toml` | `goLock = ./go2nix.toml` | Or `goLock = null`. |
| `pwd` | not needed | `src` and `modRoot` say where `go.mod` is. |
| `buildGoApplication` from an overlay | `buildGoApplication` from the scope `mkGoEnv` returns | Same name, different function. There is no overlay; the scope is the API. |
| `mkGoEnv { pwd = ./.; }`, a development shell | no equivalent; `go2nix.lib.mkGoEnv` is something else | In go2nix `mkGoEnv` makes the toolchain scope the builders live in. For a shell use `pkgs.mkShell` with the same `go`. |
| `go`, `subPackages`, `ldflags`, `tags`, `CGO_ENABLED`, `doCheck` | as in the `buildGoModule` table above | |

gomod2nix fetches each module on its own, as go2nix does, but still compiles
the application in one derivation. Everything under
[What changes](#what-changes) applies here too, apart from the vendor-hash
point.
