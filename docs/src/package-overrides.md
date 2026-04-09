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

Only the first match is used; entries are **not** merged. Module-path keys
are convenient when one module ships many cgo packages that all need the
same system libraries.

> The module-path fallback applies to **third-party** (and test) packages
> only. Local packages from your own main module are matched by exact
> import path; a key equal to your main module's path will not be applied
> to every local package.

## Supported keys

| Key | Type | Default mode | Experimental mode | Notes |
|-----|------|--------------|-------------------|-------|
| `nativeBuildInputs` | list of derivations | cgo packages only | yes | Added to the per-package derivation's `nativeBuildInputs` |
| `env` | attrset | yes | no | Extra environment variables on the per-package derivation |

In default mode, `nativeBuildInputs` from every entry in `packageOverrides`
(regardless of whether the key matched a package in the graph) are also
collected and added to the **final** application derivation, so headers and
libraries are available at link time as well.

### `nativeBuildInputs` is cgo-only

Non-cgo packages are compiled with a raw builder (`rawGoCompile`) that
bypasses stdenv entirely and hardcodes `PATH` — `nativeBuildInputs` would
silently do nothing, so the builder rejects it instead. For the error
message and fix list, see
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
