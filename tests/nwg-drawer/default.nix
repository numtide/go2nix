# Test: buildGoApplication with many GTK cgo deps (15 packages using pkg-config).
let
  pkgs = import <nixpkgs> { };
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

  goEnv = import ../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  src = pkgs.fetchFromGitHub {
    owner = "nwg-piotr";
    repo = "nwg-drawer";
    rev = "v0.7.4";
    hash = "sha256-yKRh2kAWg8GJjEJ/yCJ88JoJSgYR3c3RafeYU3z3pNU=";
  };
  goLock = ./go2nix.toml;
  pname = "nwg-drawer";
  version = "0.7.4";
  packageOverrides = {
    "github.com/diamondburned/gotk4/pkg" = gtkDeps;
    "github.com/diamondburned/gotk4-layer-shell/pkg" = gtkDeps;
  };
}
