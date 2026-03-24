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
    {
      self,
      nixpkgs,
      treefmt-nix,
    }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems =
        f: nixpkgs.lib.genAttrs systems (system: f system (import nixpkgs { inherit system; }));
    in
    {
      packages = forAllSystems (
        system: pkgs:
        let
          callPkg = path: pkgs.callPackage path { };
          callPkgWith = path: args: pkgs.callPackage path args;
          flake = self;
        in
        {
          default = callPkg ./packages/go2nix/default.nix;
          go2nix = callPkg ./packages/go2nix/default.nix;
          docs = callPkg ./packages/docs/default.nix;
          go2nix-nix-plugin = callPkg ./packages/go2nix-nix-plugin/default.nix;
          go2nix-nix-plugin-eval-test = pkgs.callPackage ./packages/go2nix-nix-plugin/tests/eval-test.nix {
            plugin = callPkg ./packages/go2nix-nix-plugin/default.nix;
            testFixtures = ./packages/go2nix-nix-plugin/tests/fixtures;
          };
          go2nix-nix-plugin-resolve-hashes-test =
            pkgs.callPackage ./packages/go2nix-nix-plugin/tests/resolve-hashes-test.nix
              {
                plugin = callPkg ./packages/go2nix-nix-plugin/default.nix;
                testFixtures = ./packages/go2nix-nix-plugin/tests/fixtures;
              };

          test-package-yubikey-agent-no-lockfile =
            callPkgWith ./packages/test-package-yubikey-agent-no-lockfile/default.nix
              {
                inherit flake system;
              };

          test-package-dotool = callPkgWith ./packages/test-package-dotool/default.nix {
            inherit flake system;
          };
          test-package-nwg-drawer = callPkgWith ./packages/test-package-nwg-drawer/default.nix {
            inherit flake system;
          };
          test-package-vinegar = callPkgWith ./packages/test-package-vinegar/default.nix {
            inherit flake system;
          };
          test-package-yubikey-agent = callPkgWith ./packages/test-package-yubikey-agent/default.nix {
            inherit flake system;
          };

          test-fixture-testify-basic = callPkgWith ./packages/test-fixture-testify-basic/default.nix {
            inherit flake system;
          };
          test-fixture-xtest-local-dep = callPkgWith ./packages/test-fixture-xtest-local-dep/default.nix {
            inherit flake system;
          };
          test-fixture-test-helper-pkg = callPkgWith ./packages/test-fixture-test-helper-pkg/default.nix {
            inherit flake system;
          };
          test-fixture-modroot-nested = callPkgWith ./packages/test-fixture-modroot-nested/default.nix {
            inherit flake system;
          };
          test-fixture-torture-app-full = callPkgWith ./packages/test-fixture-torture-app-full/default.nix {
            inherit flake system;
          };

          go2nix-testgen = callPkgWith ./packages/go2nix-testgen/default.nix {
            inherit flake system;
          };

          benchmark-build = callPkgWith ./packages/benchmark-build/default.nix {
            inherit flake system;
          };
          benchmark-build-cross-app-isolation =
            callPkgWith ./packages/benchmark-build-cross-app-isolation/default.nix
              {
                inherit flake system;
              };
          benchmark-eval = callPkgWith ./packages/benchmark-eval/default.nix {
            inherit flake system;
          };
        }
      );

      devShells = forAllSystems (
        _: pkgs: {
          default = import ./devshell.nix { inherit pkgs; };
        }
      );

      checks = forAllSystems (
        _system: pkgs:
        let
          callPkg = path: pkgs.callPackage path { };
          callPkgWith = path: args: pkgs.callPackage path args;
          treefmt = import ./formatter.nix {
            inherit pkgs;
            inputs = { inherit treefmt-nix; };
          };
        in
        {
          formatting = treefmt.check self;
          go2nix-nix-plugin-eval-test = pkgs.callPackage ./packages/go2nix-nix-plugin/tests/eval-test.nix {
            plugin = callPkg ./packages/go2nix-nix-plugin/default.nix;
            testFixtures = ./packages/go2nix-nix-plugin/tests/fixtures;
          };
          go2nix-nix-plugin-resolve-hashes-test =
            pkgs.callPackage ./packages/go2nix-nix-plugin/tests/resolve-hashes-test.nix
              {
                plugin = callPkg ./packages/go2nix-nix-plugin/default.nix;
                testFixtures = ./packages/go2nix-nix-plugin/tests/fixtures;
              };
          golangci-lint-go2nix = callPkgWith ./packages/golangci-lint-go2nix/default.nix {
            go2nix = callPkg ./packages/go2nix/default.nix;
          };
          golangci-lint-go2nix-testgen = callPkg ./packages/golangci-lint-go2nix-testgen/default.nix;
          check-godebug-table = callPkg ./packages/check-godebug-table/default.nix;
        }
      );

      formatter = forAllSystems (
        _: pkgs:
        let
          treefmt = import ./formatter.nix {
            inherit pkgs;
            inputs = { inherit treefmt-nix; };
          };
        in
        treefmt.wrapper
      );

      lib = import ./lib.nix { };
    };
}
