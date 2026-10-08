# srcFilter fixture: the real tree plus a predicate must build the same
# derivation as a src pre-filtered by builtins.path with that predicate, and
# an unfiltered build must differ (the predicate really reaches the copies).
# lib/notes.txt is a non-Go file inside a package directory, so it lands in
# lib's per-package copy (and in mainSrc, doCheck = true) unless excluded.
#
# `src` is an argument so the test can point the same builds at a copy of
# this tree that has a directory the evaluator cannot read, `lib/locked`;
# the predicate rejects that name too.
{
  src ? ./.,
}:
let
  pkgs = import <nixpkgs> { };
  inherit (pkgs) go;
  go2nix = import ../../../packages/go2nix { inherit pkgs; };
  goEnv = import ../../../nix/mk-go-env.nix {
    inherit go go2nix;
    inherit (pkgs) callPackage;
  };
  excludeNotes = path: _type: baseNameOf path != "notes.txt" && baseNameOf path != "locked";
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
      path = src;
      name = "srcfilter-prefiltered";
      filter = excludeNotes;
    };
  };
  viaSrcFilter = build {
    inherit src;
    srcFilter = excludeNotes;
  };
  unfiltered = build { inherit src; };
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
