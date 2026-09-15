# Test: a third-party package importing a module the main go.mod replaces
# with a directory.
#   - go.mod has `replace github.com/pmezard/go-difflib => ./third_party/go-difflib`
#   - main.go imports testify/assert, which imports go-difflib/difflib
# The resolver lists go-difflib/difflib as a local package, so testify/assert
# reaches it through `localImports`, not `imports`.
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  pname = "thirdparty-local-replace";
  version = "0.0.1";
  src = ./.;
  goLock = ./go2nix.toml;
}
