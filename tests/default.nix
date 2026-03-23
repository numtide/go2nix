# tests/default.nix — build all tests with `nix-build tests/`
# Use `nix-build tests/ -A <pkg>.<mode>` for a specific mode (default, dag, dynamic).
{
  yubikey-agent = {
    default = import ./packages/yubikey-agent;
    dag = import ./packages/yubikey-agent/dag.nix;
    dynamic = import ./packages/yubikey-agent/dynamic.nix;
  };
  dotool = {
    default = import ./packages/dotool;
    dag = import ./packages/dotool/dag.nix;
    dynamic = import ./packages/dotool/dynamic.nix;
  };
  nwg-drawer = {
    default = import ./packages/nwg-drawer;
    dag = import ./packages/nwg-drawer/dag.nix;
    dynamic = import ./packages/nwg-drawer/dynamic.nix;
  };
  fixtures = {
    testify-basic = import ./fixtures/testify-basic/dag.nix;
    xtest-local-dep = import ./fixtures/xtest-local-dep/dag.nix;
    modroot-nested = import ./fixtures/modroot-nested/dag.nix;
  };
}
