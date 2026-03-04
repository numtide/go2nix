# go2nix/lib.nix — thin re-export of modular components.
#
# Public API:
#   buildGoStdlib { go, runCommandCC }
#   importcfgFor  { stdlib, deps }
#   mkGoPackageSet { goLock, go, go2nix, pkgs, ... }
#   buildGoBinary  { src, go, go2nix, pkgs, ... }
{ }:
{
  buildGoStdlib = args: import ./nix/stdlib.nix args;
  importcfgFor = args: import ./nix/importcfg.nix args;
  mkGoPackageSet = args: import ./nix/mk-go-package-set.nix args;
  buildGoBinary = args: import ./nix/build-go-binary.nix args;
}
