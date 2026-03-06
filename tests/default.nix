# tests/default.nix — build all tests with `nix-build tests/`
{
  yubikey-agent = import ./packages/yubikey-agent;
  dotool = import ./packages/dotool;
  nwg-drawer = import ./packages/nwg-drawer;
}
