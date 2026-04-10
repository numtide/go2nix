# Build test: yubikey-agent via buildGoApplicationExperimental (cgo + pcsclite).
{
  flake,
  pkgs,
  ...
}:
import ../test-lib/run-dynamic-test.nix {
  inherit pkgs;
  testName = "yubikey-agent";
  dynamicFile = "${flake}/tests/packages/yubikey-agent/dynamic.nix";
}
