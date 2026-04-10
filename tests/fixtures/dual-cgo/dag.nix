# Regression: scope-level goEnv.CGO_ENABLED must reach the eval-time
# `go list` (resolveGoPackages) so packages with both `//go:build cgo` and
# `//go:build !cgo` files are classified consistently with the
# CGO_ENABLED=0 stdlib. Without that, the plugin sees the cgo file and
# emits isCgo=true, then compile fails with `could not import runtime/cgo`.
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
    # Scope-level — not the buildGoApplication CGO_ENABLED arg.
    goEnv = {
      CGO_ENABLED = "0";
    };
  };
in
goEnv.buildGoApplication {
  pname = "dual-cgo";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
  doCheck = false;
}
