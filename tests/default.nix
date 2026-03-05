# tests/default.nix — build all tests with `nix-build tests/`
{
  pkgs ? import <nixpkgs> { },
}:
{
  compile-pkg = import ./compile-pkg.nix;
  link = import ./link.nix;
  mkgopackageset = import ./mkgopackageset.nix;
  yubikey-agent = import ./yubikey-agent;
  dotool = import ./dotool;
  nwg-drawer = import ./nwg-drawer;
}
