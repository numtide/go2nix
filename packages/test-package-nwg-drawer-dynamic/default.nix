# Build test: nwg-drawer via buildGoApplicationExperimental (heavy GTK cgo).
{
  flake,
  pkgs,
  ...
}:
import ../test-lib/run-dynamic-test.nix {
  inherit pkgs;
  testName = "nwg-drawer";
  dynamicFile = "${flake}/tests/packages/nwg-drawer/dynamic.nix";
}
