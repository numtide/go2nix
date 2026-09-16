# Package Overrides

`packageOverrides` lets you customize the per-package derivation for
specific Go packages — typically to give a cgo package the C toolchain
inputs it needs.

This page covers the lookup rules, the supported keys, and worked examples.
For the one-line summary, see the [Builder API](builder-api.md) table.

## Lookup order

`packageOverrides` is keyed by **import path or module path**. When
compiling a package, the builder looks up an override in this order:

1. The package's exact import path
   (e.g. `"github.com/diamondburned/gotk4/pkg/core/glib"`).
1. The package's module path (e.g. `"github.com/diamondburned/gotk4/pkg"`).
1. Otherwise, no override.

In default mode only the first match is used; entries are **not** merged.
(The experimental builder applies both: the `nativeBuildInputs` of the
import-path entry and of the module-path entry are added together.)
Module-path keys are convenient when one module ships many cgo packages that
all need the same system libraries.

> In default mode the module-path fallback applies to local packages too: a
> key equal to your main module's path, or to the path of a module you
> `replace` with a directory, is applied to every local package of that
> module. In experimental mode local packages are matched by exact import
> path only.

## Supported keys

| Key | Type | Default mode | Experimental mode | Notes |
|-----|------|--------------|-------------------|-------|
| `nativeBuildInputs` | list of derivations | cgo packages only | yes | Default mode: added to the per-package derivation's `nativeBuildInputs`. Experimental mode: their `bin`, `lib/pkgconfig` and `include` directories are put on `PATH`, `PKG_CONFIG_PATH` and `CGO_CFLAGS`, and a derivation's `dev` output is added automatically |
| `env` | attrset | yes | no | Extra environment variables on the per-package derivation |
| `srcOverlay` | derivation or path | yes | no | Contents are layered onto the package's source directory at build time |

In default mode, `nativeBuildInputs` from every entry in `packageOverrides`
(regardless of whether the key matched a package in the graph) are also
collected and added to the **final** application derivation, so headers and
libraries are available at link time as well.

### `nativeBuildInputs` is cgo-only

Non-cgo packages are compiled with a raw builder (`rawGoCompile`) that
bypasses stdenv entirely and hardcodes `PATH` — `nativeBuildInputs` would
silently do nothing. When the key is that package's exact import path the
builder rejects it instead; when the entry matched through the module path,
which legitimately covers cgo and non-cgo packages alike, it is dropped for
the non-cgo ones without an error. For the error message and fix list, see
[Troubleshooting](troubleshooting.md#packageoverridespath-unknown-attributes-nativebuildinputs).

## Example: single cgo package

`dotool` has one local cgo package wrapping libxkbcommon via pkg-config:

```nix
goEnv.buildGoApplication {
  pname = "dotool";
  version = "1.6";
  src = ./.;
  goLock = ./go2nix.toml;

  packageOverrides = {
    "git.sr.ht/~geb/dotool/xkb" = {
      nativeBuildInputs = [
        pkgs.pkg-config
        pkgs.libxkbcommon
      ];
    };
  };
}
```

## Example: many cgo packages from one module

`gotk4` ships dozens of cgo packages under one module. Key the override by
the **module path** so it applies to every package in that module:

```nix
let
  gtkDeps = {
    nativeBuildInputs = [
      pkgs.pkg-config
      pkgs.glib
      pkgs.cairo
      pkgs.gobject-introspection
      pkgs.gdk-pixbuf
      pkgs.pango
      pkgs.gtk3
      pkgs.at-spi2-core
      pkgs.gtk-layer-shell
    ];
  };
in
goEnv.buildGoApplication {
  pname = "nwg-drawer";
  version = "0.7.4";
  src = ./.;
  goLock = ./go2nix.toml;

  packageOverrides = {
    "github.com/diamondburned/gotk4/pkg" = gtkDeps;
    "github.com/diamondburned/gotk4-layer-shell/pkg" = gtkDeps;
  };
}
```

## Example: `env`

```nix
packageOverrides = {
  "github.com/example/pkg" = {
    env = {
      CGO_CFLAGS = "-I${libfoo.dev}/include";
    };
  };
};
```

`env` is default-mode only. The experimental builder synthesizes
derivations at build time inside `go2nix resolve` and can only forward
store paths, so it rejects `env` (and any other unknown key) at eval time.

## Example: `srcOverlay` for build-time-generated `//go:embed` content

When a package embeds files that are themselves build outputs — a bundled
SPA under `//go:embed all:dist`, generated protobuf descriptors, etc. —
`srcOverlay` lets you supply them as a derivation that is layered onto the
package's source directory at build time:

```nix
packageOverrides = {
  "example.com/app/ui" = {
    srcOverlay = pkgs.runCommand "ui-dist" { } ''
      mkdir -p $out/dist
      cp -r ${uiBundle}/* $out/dist/
    '';
  };
};
```

The overlay is `cp -rL`'d onto a writable copy of the package's source
before `go2nix compile-package` runs, so `ResolveEmbedCfg` sees the
generated files. This is a build-time input of that one compile derivation
only — it does **not** flow into `src` at evaluation time, so it does not
trigger IFD and does not invalidate sibling packages.

Keep a placeholder file in the source tree (e.g. `ui/dist/.gitkeep`) so
the eval-time `go list` doesn't error on a `//go:embed` pattern with zero
matches; the overlay then replaces or augments it at build time.

The `doCheck` testrunner applies the same overlay before resolving
`//go:embed` patterns and recompiling for tests, so tests see the
overlaid content too.
