# Recipes

Worked setups for the situations that come up after the
[first build](getting-started.md). The derivation counts and "nothing
rebuilds" claims below were checked by building the layouts shown.

## A module inside a larger repository

```
repo/
├── flake.nix
├── docs/  web/  …                  # not Go
├── libs/greet/                     # module example.com/libs/greet
│   ├── go.mod
│   └── greet.go
└── services/api/                   # module example.com/api
    ├── go.mod                      # replace example.com/libs/greet => ../../libs/greet
    ├── go2nix.toml
    ├── cmd/api/main.go
    └── internal/handlers/…
```

```nix
packages.api = goEnv.buildGoApplication {
  pname = "api";
  version = "0.1.0";
  src = ./.;                         # the repository root
  modRoot = "services/api";          # where go.mod is
  subPackages = [ "cmd/api" ];       # relative to modRoot
  goLock = ./services/api/go2nix.toml;
};
```

`src` has to contain everything the module's filesystem `replace` directives
point at, which is why it is the repository root and not `./services/api`.
`result/bin/api` is named after the directory of the main package.

Generate the lockfile from inside the module, because `-o` is relative to
where you run the command:

```bash
cd services/api && nix run github:numtide/go2nix -- generate .
```

Modules reached through a filesystem `replace` are not in the lockfile: their
packages are compiled as local packages (`golocal-example.com-libs-greet`),
straight from `src`.

**What an unrelated change costs.** Nothing is rebuilt. Every package
derivation is keyed on its own directory, and the final derivation on a
filtered copy that holds only the packages in the build and the `go.mod` and
`go.sum` of the modules involved. Editing `docs/` and `web/` and adding a whole
new `services/billing/` module to the layout above gives the same derivation,
store path for store path. Nix still evaluates, which means it still runs
`go list`; see [Incremental Builds](incremental-builds.md#the-cost-eval-time).

**Do you need to narrow `src`?** Not for caching. Building with `src`
restricted to the two module directories produces the identical derivation.
What narrowing saves is the copy of the repository into the store when Nix
evaluates a path outside a flake. If that matters,
`helpers.goModLocalReplaceDirs` follows the `replace` directives for you:

```nix
src = lib.fileset.toSource {
  root = ./.;
  fileset = lib.fileset.unions
    (goEnv.helpers.goModLocalReplaceDirs ./services/api);
};
```

`srcFilter = path: type: …` is the other tool: a predicate applied to every
per-package copy and to the final one, for files that sit *inside* package
directories and should not count (editor backups, generated reports).
[Builder API](builder-api.md) has both.

**Several services.** Build them all from one `goEnv`. Third-party packages
are derivations of their own, so `api` and `billing` compile
`golang.org/x/sys/unix` once between them, whatever their lockfiles say, as
long as they agree on the version, `tags` and `gcflags`.

## Private modules, end to end

Module downloads happen in three places, each with its own environment. A
private module has to be reachable in all three.

| Where | When | Sees |
|-------|------|------|
| `go2nix generate` | you run it | your whole environment: `GOPROXY`, `GOPRIVATE`, `~/.netrc`, your `git` and its credentials. It turns the checksum database off itself. |
| `go list`, in the plugin | every evaluation | `GOMODCACHE`, `GOPATH`, `HOME` (so `~/.netrc`), `GOPROXY`, `NETRC` and TLS certificate variables of whatever evaluates; `goProxy` overrides `GOPROXY`. Not `GOPRIVATE`, and no `go env -w` settings. |
| module fetch derivations | the build | `GOPROXY` and `NETRC` of whatever *runs the build* (the daemon, a remote builder), unless `goProxy` is set; the scope's `netrcFile`; no `git`. |

The setup that works everywhere is a Go module proxy that serves your private
modules, plus credentials for it:

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  netrcFile = ./ci/netrc;            # machine goproxy.example.com login … password …
};

packages.api = goEnv.buildGoApplication {
  # …
  goProxy = "https://goproxy.example.com,https://proxy.golang.org";
};
```

- `goProxy` puts the proxy in the fetch derivations themselves, so it does
  not depend on how the daemon was started, and it is also what the
  evaluation-time `go list` uses.
- `netrcFile` ends up in the store, readable by every user of the machine and
  of any cache you push to. Use a token that can only read those modules. See
  [Builder API](builder-api.md#private-modules-netrcfile).
- For evaluation, the machine needs the same credentials in `~/.netrc` (or
  `NETRC`), or the modules already in `GOMODCACHE`. A CI job that runs
  `go mod download` before `nix build` satisfies that.
- Fetching straight from a private Git host (`GOPRIVATE`, `direct`) works for
  `generate` on your machine and nowhere else: the fetch derivations have no
  `git`. Put a proxy in front (Athens, Artifactory, the forge's own Go
  registry).

When a fetch fails with a 404 or a 401, the derivation is named
`gomod-<module>-<version>`, and its log is `go mod download`'s own output.

## cgo packages that need a system library

[Package Overrides](package-overrides.md) has the examples. Finding *which*
package to override is the part that is not obvious: the build fails in a
derivation named `gopkg-<import path>-<version>`, and that import path, with
the dashes turned back into slashes, is the key. `nix log` on that derivation
shows the missing header or `pkg-config` complaint.

```nix
packageOverrides."github.com/go-piv/piv-go/piv" = {
  nativeBuildInputs = [ pkgs.pkg-config pkgs.pcsclite ];
};
```

A key that is a module path applies to every cgo package of that module. The
libraries a cgo package links against also have to be present when the final
binary is linked; the builder adds each override's `nativeBuildInputs` to the
application derivation for that.

## A static binary for a container

```nix
goEnv = go2nix.lib.mkGoEnv {
  inherit (pkgs) go callPackage;
  go2nix = go2nix.packages.${system}.go2nix;
  goEnv.CGO_ENABLED = "0";
};

packages.api = goEnv.buildGoApplication {
  # …
  ldflags = [ "-s" "-w" ];
};
```

`CGO_ENABLED` belongs in the scope's `goEnv` because the standard library is
compiled once per scope and has to agree with the packages built on top of
it. With cgo off, `net` and `os/user` use their pure-Go implementations and
the binary has no dynamic dependencies; its closure is the binary (the builder
refuses a result that still refers to the Go toolchain unless
`allowGoReference` is set).
