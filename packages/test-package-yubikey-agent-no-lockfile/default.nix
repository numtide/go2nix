# Build test: yubikey-agent without lockfile (hashes from plugin).
{ flake, pkgs, system, ... }:
import ../test-lib/run-dag-test.nix {
  inherit flake pkgs system;
  testName = "yubikey-agent-no-lockfile";
  src = pkgs.fetchFromGitHub {
    owner = "FiloSottile";
    repo = "yubikey-agent";
    rev = "v0.1.6";
    hash = "sha256-Knk1ipBOzjmjrS2OFUMuxi1TkyDcSYlVKezDWT//ERY=";
  };
  vendorHash = "sha256-HtPnM5Z1e1fMoTlZt5MxK/i32kpECREjVDfNw6rNAsM=";
  dagFile = "${flake}/tests/packages/yubikey-agent/dag-no-lockfile.nix";
  checkCommand = "$result/bin/yubikey-agent --help";
}
