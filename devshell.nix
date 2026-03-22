{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.go_1_26
    pkgs.time
    pkgs.mdbook
  ];

  shellHook = ''
    export PRJ_ROOT=$PWD
    export NIX_PATH=nixpkgs=${pkgs.path}
  '';
}
