{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.go
  ];

  shellHook = ''
    export PRJ_ROOT=$PWD
    export NIX_PATH=nixpkgs=${pkgs.path}
  '';
}
