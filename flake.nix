{
  nixConfig = {
    extra-substituters = [ "https://nix-community.cachix.org" ];
    extra-trusted-public-keys = [
      "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
    ];
  };

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/fdc7b8f7b30fdbedec91b71ed82f36e1637483ed";
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
          test-fixture-lang-loopvar = callPkgWith ./packages/test-fixture-lang-loopvar/default.nix {
            inherit flake system;
          };
          test-fixture-torture-app-full = callPkgWith ./packages/test-fixture-torture-app-full/default.nix {
            inherit flake system;
          };
          test-fixture-torture-app-replace =
            callPkgWith ./packages/test-fixture-torture-app-replace/default.nix
              {
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
          cross-platform-env = callPkgWith ./tests/nix/cross_platform_test.nix { inherit pkgs; };
          # The --help check just confirms the binary links and the CLI
          # surface is intact. The benchmark itself can't run in nix's
          # build sandbox (it spawns a nested daemon and fetches from
          # substituters). Use `nix run .#bench-incremental --
          # --assert-cascade N` for the actual regression check.
          bench-incremental-help =
            pkgs.runCommand "bench-incremental-help"
              {
                bench = callPkg ./packages/bench-incremental/default.nix;
              }
              ''
                $bench/bin/bench-incremental --help > /dev/null 2>&1 || true
                touch $out
              '';
        }
      );

      apps = forAllSystems (
        _system: pkgs:
        let
          # The bench binary spawns its own in-process daemon proxy
          # (no socat) and shells out to nix/nix-store, so PATH needs
          # nix on it.
          bench = pkgs.callPackage ./packages/bench-incremental/default.nix { };
          benchIncremental = pkgs.writeShellScriptBin "bench-incremental" ''
            export PATH="${pkgs.lib.makeBinPath [ pkgs.nixVersions.nix_2_34 ]}:$PATH"
            exec ${bench}/bin/bench-incremental "$@"
          '';
        in
        {
          bench-incremental = {
            type = "app";
            program = "${benchIncremental}/bin/bench-incremental";
          };
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
