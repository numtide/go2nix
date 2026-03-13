# Test: buildGoApplicationVendorMode (explicit) with many GTK cgo deps (15 packages using pkg-config).
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplicationVendorMode {
  src = pkgs.fetchFromGitHub {
    owner = "nwg-piotr";
    repo = "nwg-drawer";
    rev = "v0.7.4";
    hash = "sha256-yKRh2kAWg8GJjEJ/yCJ88JoJSgYR3c3RafeYU3z3pNU=";
  };
  goLock = ./go2nix-vendor.toml;
  pname = "nwg-drawer";
  version = "0.7.4";
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
}
