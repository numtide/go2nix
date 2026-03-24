# Build test: vinegar (puregotk, no CGO for GTK libs).
{
  flake,
  pkgs,
  system,
  ...
}:
import ../test-lib/run-dag-test.nix {
  inherit flake pkgs system;
  testName = "vinegar";
  src = pkgs.fetchFromGitHub {
    owner = "vinegarhq";
    repo = "vinegar";
    rev = "v1.9.3";
    hash = "sha256-0MNUkfhbsvOJdN89VGTuf3zHUFhimiCNuoY47V03Cgo=";
  };
  vendorHash = "sha256-jlJN8AZT1WpMTbqrZg8PAnjI5TwoIkAm7gATn0nc8CA=";
  dagFile = "${flake}/tests/packages/vinegar/dag.nix";
  checkCommand = "test -x $result/bin/vinegar";
}
