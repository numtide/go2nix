{ pkgs }:
pkgs.stdenvNoCC.mkDerivation {
  pname = "go2nix-docs";
  version = "0-unstable";
  src = ../../docs;
  nativeBuildInputs = [ pkgs.mdbook ];
  buildPhase = "mdbook build";
  installPhase = "cp -r book $out";
}
