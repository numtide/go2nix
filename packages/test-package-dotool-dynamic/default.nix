# Build test: dotool via buildGoApplicationExperimental.
{
  flake,
  pkgs,
  ...
}:
import ../test-lib/run-dynamic-test.nix {
  inherit pkgs;
  testName = "dotool";
  dynamicFile = "${flake}/tests/packages/dotool/dynamic.nix";
}
