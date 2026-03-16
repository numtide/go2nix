{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.go
    pkgs.time
    pkgs.mdbook
  ];

  shellHook = ''
    export PRJ_ROOT=$PWD
    export NIX_PATH=nixpkgs=${pkgs.path}
  '';
}
