# Test: buildGoApplicationDynamicMode with many GTK cgo deps (15 packages using pkg-config).
#
# Requires: recursive-nix, ca-derivations, dynamic-derivations experimental features.
# Requires: Nix >= 2.34 (v4 derivation JSON format).
# Run: nix-build tests/packages/nwg-drawer/dynamic.nix
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
    nixPackage = pkgs.nixVersions.git;
  };

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
in
goEnv.buildGoApplicationDynamicMode {
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
