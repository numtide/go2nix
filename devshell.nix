{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.go
    pkgs.cargo
    pkgs.rustc
    pkgs.rustfmt
    pkgs.binaryen
    pkgs.lld
    pkgs.time
  ];

  shellHook = ''
    export PRJ_ROOT=$PWD
    export NIX_PATH=nixpkgs=${pkgs.path}
  '';
}
