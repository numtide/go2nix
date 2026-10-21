# shellcheck shell=bash
# Assertions on one binary of tests/fixtures/stdlib-per-build, shared by
# test-fixture-stdlib-per-build and test-fixture-stdlib-per-build-dynamic.
# Needs `file` and `go` on PATH. The linkage check reads what `file` says
# about an ELF binary, so it only passes on Linux.

# Name of the build being checked, for the messages; set by the caller.
variant=

fail() {
  echo "FAIL: $variant: $*" >&2
  exit 1
}

# present <regex> <yes|no>: whether nm.txt has a symbol matching the regex.
present() {
  local got=no
  grep -q -E "$1" nm.txt && got=yes
  [ "$got" = "$2" ] || fail "symbols matching '$1': got $got, want $2"
}

# inspectBinary <binary> <output> <linkage> <CGO_ENABLED> <-tags or ""> <net cgo> <os/user cgo> <tzdata>
#
# The last three say (yes|no) whether the binary must contain the cgo
# resolver of net, the cgo lookups of os/user, and time/tzdata.
inspectBinary() {
  local bin=$1 wantOut=$2 wantLinkage=$3 wantCgo=$4 wantTags=$5 got

  got=$("$bin")
  [ "$got" = "$wantOut" ] || fail "binary printed '$got', want '$wantOut'"

  file -b "$bin" | tee file.txt
  grep -q "$wantLinkage" file.txt || fail "want a $wantLinkage binary"

  go version -m "$bin" | tee buildinfo.txt
  grep -q -P "^\tbuild\tCGO_ENABLED=$wantCgo\$" buildinfo.txt ||
    fail "build info does not say CGO_ENABLED=$wantCgo"
  if [ -n "$wantTags" ]; then
    grep -q -P "^\tbuild\t-tags=$wantTags\$" buildinfo.txt ||
      fail "build info does not say -tags=$wantTags"
  fi

  go tool nm "$bin" >nm.txt
  present ' main\.main$' yes
  present ' net\._C' "$6"
  present ' os/user\._C' "$7"
  present ' time/tzdata\.' "$8"
}
