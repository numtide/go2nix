{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.go
  ];

  shellHook = ''
    export PRJ_ROOT=$PWD
    export NIXPKGS=${pkgs.path}
  '';
}
