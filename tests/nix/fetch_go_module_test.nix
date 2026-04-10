# tests/nix/fetch_go_module_test.nix — unit tests for nix/dag/fetch-go-module.nix
#
# Run: nix eval -f tests/nix/fetch_go_module_test.nix --impure
# Returns true on success, throws on failure.
let
  pkgs = import <nixpkgs> { };
  fetcher = pkgs.callPackage ../../nix/dag/fetch-go-module.nix {
    inherit (pkgs) go;
    helpers = import ../../nix/helpers.nix;
    netrcFile = null;
  };
  drv = fetcher {
    hash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
    fetchPath = "example.com/test/module";
    version = "v1.0.0";
  };
  drvWithProxy = fetcher {
    hash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
    fetchPath = "example.com/test/module";
    version = "v1.0.0";
    goProxy = "https://proxy.example/go";
  };

  assertElem =
    name: x: xs:
    if builtins.elem x xs then
      true
    else
      builtins.throw "${name}: expected ${x} in [${builtins.concatStringsSep ", " xs}]";
in
assert assertElem "impureEnvVars has GOPROXY" "GOPROXY" drv.impureEnvVars;
assert assertElem "impureEnvVars has NETRC" "NETRC" drv.impureEnvVars;
assert assertElem "impureEnvVars has http_proxy" "http_proxy" drv.impureEnvVars;
# Default (goProxy = null): buildPhase must not mention GOPROXY so the
# .drv path is unchanged — the impure path is the only route.
assert
  builtins.match ".*GOPROXY.*" drv.buildPhase == null
  || builtins.throw "default buildPhase should not export GOPROXY";
# Explicit goProxy: buildPhase exports it so the FOD doesn't depend on the
# daemon's environment under daemon nix / remote builders.
assert
  pkgs.lib.hasInfix "export GOPROXY=https://proxy.example/go" drvWithProxy.buildPhase
  || builtins.throw "goProxy not exported in buildPhase";
true
