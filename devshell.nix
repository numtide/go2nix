{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.go_1_26
    pkgs.golangci-lint
    pkgs.time
    pkgs.mdbook
    pkgs.socat
    pkgs.hyperfine
  ];

  shellHook = ''
    export PRJ_ROOT=$PWD
    export NIX_PATH=nixpkgs=${pkgs.path}
  '';
}
