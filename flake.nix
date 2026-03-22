{
  nixConfig = {
    extra-substituters = [ "https://nix-community.cachix.org" ];
    extra-trusted-public-keys = [
      "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
    ];
  };

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/a07d4ce6bee67d7c838a8a5796e75dff9caa21ef";
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { self, nixpkgs, treefmt-nix }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system (import nixpkgs { inherit system; }));
    in
    {
      packages = forAllSystems (system: pkgs:
        let
          callPkg = path: pkgs.callPackage path { };
          callPkgWith = path: args: pkgs.callPackage path args;
          flake = self;
        in
        {
          default = callPkg ./packages/go2nix/default.nix;
          go2nix = callPkg ./packages/go2nix/default.nix;
          docs = callPkg ./packages/docs/default.nix;
          go-nix-plugin = callPkg ./packages/go-nix-plugin/default.nix;
          go-nix-plugin-eval-test = pkgs.callPackage ./packages/go-nix-plugin/tests/eval-test.nix {
            plugin = callPkg ./packages/go-nix-plugin/default.nix;
            testFixtures = ./packages/go-nix-plugin/tests/fixtures;
          };

          test-dag-package-dotool = callPkgWith ./packages/test-dag-package-dotool/default.nix {
            inherit flake system;
          };
          test-dag-package-nwg-drawer = callPkgWith ./packages/test-dag-package-nwg-drawer/default.nix {
            inherit flake system;
          };
          test-dag-package-vinegar = callPkgWith ./packages/test-dag-package-vinegar/default.nix {
            inherit flake system;
          };
          test-dag-package-yubikey-agent = callPkgWith ./packages/test-dag-package-yubikey-agent/default.nix {
            inherit flake system;
          };

          test-dag-fixture-testify-basic = callPkgWith ./packages/test-dag-fixture-testify-basic/default.nix {
            inherit flake system;
          };
          test-dag-fixture-xtest-local-dep = callPkgWith ./packages/test-dag-fixture-xtest-local-dep/default.nix {
            inherit flake system;
          };
          test-dag-fixture-test-helper-pkg = callPkgWith ./packages/test-dag-fixture-test-helper-pkg/default.nix {
            inherit flake system;
          };
          test-dag-fixture-modroot-nested = callPkgWith ./packages/test-dag-fixture-modroot-nested/default.nix {
            inherit flake system;
          };
        }
      );

      devShells = forAllSystems (_: pkgs: {
        default = import ./devshell.nix { inherit pkgs; };
      });

      formatter = forAllSystems (_: pkgs:
        import ./formatter.nix {
          inherit pkgs;
          inputs = { inherit treefmt-nix; };
        }
      );

      lib = import ./lib.nix { };
    };
}
