# Test: default mode with test-only local package (testutil helper).
# NOTE: doCheck is disabled because test-only local packages are not yet
# included in goLocalArchives/testDepsImportcfg. The build itself works;
# enabling doCheck requires plugin + DAG builder changes to handle
# local packages only reachable from test import graphs.
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
  pname = "test-helper-pkg";
  version = "0.0.1";
  src = ./. ;
  goLock = ./go2nix.toml;
  doCheck = false; # TODO: enable once test-only local packages are supported
}
