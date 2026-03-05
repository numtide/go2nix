# Test: buildGoBinary with many GTK cgo deps (15 packages using pkg-config).
let
  pkgs = import <nixpkgs> { };
  go2nixLib = import ../../lib.nix { };
  go = pkgs.go;
  go2nix = import ../../go/go2nix/package.nix { inherit pkgs; };

  # All GTK-related nativeBuildInputs needed by gotk4 cgo packages.
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

  # All gotk4 sub-packages that use cgo.
  gotk4CgoPkgs = [
    "core/gbox"
    "core/intern"
    "core/glib"
    "cairo"
    "core/gerror"
    "core/gextras"
    "core/gcancel"
    "glib/v2"
    "gio/v2"
    "gdkpixbuf/v2"
    "pango"
    "gdk/v3"
    "atk"
    "gtk/v3"
  ];

  gotk4Overrides = builtins.listToAttrs (map (sub: {
    name = "github.com/diamondburned/gotk4/pkg/${sub}";
    value = gtkDeps;
  }) gotk4CgoPkgs);
in
go2nixLib.buildGoBinary {
  src = pkgs.fetchFromGitHub {
    owner = "nwg-piotr";
    repo = "nwg-drawer";
    rev = "v0.7.4";
    hash = "sha256-yKRh2kAWg8GJjEJ/yCJ88JoJSgYR3c3RafeYU3z3pNU=";
  };
  goLock = ./go2nix.toml;
  pname = "nwg-drawer";
  version = "0.7.4";
  inherit go go2nix pkgs;
  packageOverrides = gotk4Overrides // {
    "github.com/diamondburned/gotk4-layer-shell/pkg/gtklayershell" = gtkDeps;
  };
}
