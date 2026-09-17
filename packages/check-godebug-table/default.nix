# Verify godebugTable matches upstream Go toolchain.
#
# Runs the check script that compares
# go/go2nix/pkg/buildinfo/godebug.go against
# $GOROOT/src/internal/godebugs/table.go.
{ pkgs }:
let
  go = pkgs.go_1_26;
in
pkgs.runCommand "check-godebug-table"
  {
    nativeBuildInputs = [ go ];
    src = pkgs.lib.sources.cleanSource ../..;
  }
  ''
    export HOME=$(mktemp -d)
    export GOCACHE=$HOME/.cache/go-build

    cd $src
    go run scripts/check-godebug-table.go

    touch $out
  ''
