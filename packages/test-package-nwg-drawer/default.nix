# Build test: nwg-drawer (cgo with GTK3/GTK Layer Shell).
{ flake, pkgs, system, ... }:
import ../test-lib/run-dag-test.nix {
  inherit flake pkgs system;
  testName = "nwg-drawer";
  src = pkgs.fetchFromGitHub {
    owner = "nwg-piotr";
    repo = "nwg-drawer";
    rev = "v0.7.4";
    hash = "sha256-yKRh2kAWg8GJjEJ/yCJ88JoJSgYR3c3RafeYU3z3pNU=";
  };
  vendorHash = "sha256-eIKDMUr4kAav+D2Lmb8Bh6bZUvat9TgHLNVewi3JBYo=";
  dagFile = "${flake}/tests/packages/nwg-drawer/dag.nix";
}
