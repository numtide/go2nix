# Test: buildGoApplication (explicit) with puregotk (dlopen, no CGO for GTK libs).
#
# Vinegar embeds a GLib resource bundle via go:embed, so we must compile
# vinegar.gresource into the source tree before go2nix can analyse the
# package graph (builtins.resolveGoPackages runs `go list` at eval time).
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };

  vinegar-src = pkgs.fetchFromGitHub {
    owner = "vinegarhq";
    repo = "vinegar";
    rev = "v1.9.3";
    hash = "sha256-0MNUkfhbsvOJdN89VGTuf3zHUFhimiCNuoY47V03Cgo=";
  };

  # Pre-compile the GLib resource bundle so go:embed can find it.
  src = pkgs.runCommand "vinegar-src-with-gresource" {
    nativeBuildInputs = [ pkgs.glib pkgs.libxml2 ];
  } ''
    cp -r ${vinegar-src} $out
    chmod -R u+w $out
    glib-compile-resources \
      --sourcedir=$out/data \
      --target=$out/internal/gutil/vinegar.gresource \
      $out/data/vinegar.gresource.xml
  '';
in
goEnv.buildGoApplication {
  inherit src;
  goLock = ./go2nix.toml;
  pname = "vinegar";
  version = "1.9.3";
  subPackages = [ "./cmd/vinegar" ];
  # puregotk's init() calls pkg-config to locate GTK4 libs at runtime;
  # tests that transitively import puregotk need these in the sandbox.
  nativeCheckInputs = with pkgs; [ pkg-config gtk4 graphene libadwaita gtk-layer-shell ];
}
