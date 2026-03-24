# Build test: dotool (cgo with xkbcommon via pkg-config).
{
  flake,
  pkgs,
  system,
  ...
}:
import ../test-lib/run-dag-test.nix {
  inherit flake pkgs system;
  testName = "dotool";
  src = pkgs.fetchgit {
    url = "https://git.sr.ht/~geb/dotool";
    rev = "180af21c46dcc848d93dbec2644c011f4eea1592";
    hash = "sha256-KI3vA45/MvFRV8Fr3Q4yd/argDy1PpFHCT3KA9VDP80=";
  };
  vendorHash = "sha256-cdW4DhPUubSvJ9edTYgum3ppEkEPuoYFrU+gonyCpOk=";
  dagFile = "${flake}/tests/packages/dotool/dag.nix";
  checkCommand = "$result/bin/dotool --version";
}
