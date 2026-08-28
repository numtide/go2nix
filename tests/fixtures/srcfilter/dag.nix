# srcFilter fixture: the real tree plus a predicate must build the same
# derivation as a src pre-filtered by builtins.path with that predicate, and
# an unfiltered build must differ (the predicate really reaches the copies).
# lib/notes.txt is a non-Go file inside a package directory, so it lands in
# lib's per-package copy (and in mainSrc, doCheck = true) unless excluded.
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
  excludeNotes = path: _type: baseNameOf path != "notes.txt";
  build =
    args:
    goEnv.buildGoApplication (
      {
        pname = "srcfilter";
        version = "0.0.1";
        subPackages = [ "./cmd/app" ];
        doCheck = true;
      }
      // args
    );
  prefiltered = build {
    src = builtins.path {
      path = ./.;
      name = "srcfilter-prefiltered";
      filter = excludeNotes;
    };
  };
  viaSrcFilter = build {
    src = ./.;
    srcFilter = excludeNotes;
  };
  unfiltered = build { src = ./.; };
in
{
  inherit prefiltered viaSrcFilter unfiltered;
  check =
    assert prefiltered.outPath == viaSrcFilter.outPath;
    assert unfiltered.outPath != viaSrcFilter.outPath;
    pkgs.runCommand "srcfilter-check" { } ''
      echo "srcFilter == pre-filtered src: ${viaSrcFilter}" > $out
    '';
}
