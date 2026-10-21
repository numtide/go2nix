# The builds of this fixture, shared by dag.nix and dynamic.nix.
#
#   default  nothing set: the scope's standard library
#   cgo0     CGO_ENABLED = 0 on the build, nothing on the scope
#   tags     tags = [ netgo osusergo timetzdata ] on the build
#   cgo1     CGO_ENABLED = 1 on the build over goEnv.CGO_ENABLED = "0" on
#            the scope: the build's value wins, and dualcgo is compiled as
#            a cgo package
#   unread   a tag no file of the standard library is constrained by: the
#            default builder keeps the scope's standard library
#   netgo    tags = [ netgo ]: a standard library of its own
#   mixed    netgo next to the unread tag: the standard library of netgo
#   skew     the unread tag on a scope whose `go` names another release
#            than the plugin's toolchain: what the plugin's standard library
#            reads does not count, and the build gets one of its own
#
# `scopeGoEnv` goes to mkGoEnv, `build` to the builder. `env` tells
# main_test.go which standard-library functions the test binary must
# contain. `goVersionSuffix` is added to the `version` of the scope's `go`.
{
  default = {
    env = {
      WANT_NET_CGO = "1";
      WANT_USER_CGO = "1";
      WANT_TZDATA = "0";
    };
  };
  cgo0 = {
    build.CGO_ENABLED = 0;
    env = {
      WANT_NET_CGO = "0";
      WANT_USER_CGO = "0";
      WANT_TZDATA = "0";
    };
  };
  tags = {
    build.tags = [
      "netgo"
      "osusergo"
      "timetzdata"
    ];
    env = {
      WANT_NET_CGO = "0";
      WANT_USER_CGO = "0";
      WANT_TZDATA = "1";
    };
  };
  cgo1 = {
    scopeGoEnv.CGO_ENABLED = "0";
    build.CGO_ENABLED = 1;
    env = {
      WANT_NET_CGO = "1";
      WANT_USER_CGO = "1";
      WANT_TZDATA = "0";
    };
  };
  unread = {
    build.tags = [ "unread_by_std" ];
    env = {
      WANT_NET_CGO = "1";
      WANT_USER_CGO = "1";
      WANT_TZDATA = "0";
    };
  };
  netgo = {
    build.tags = [ "netgo" ];
    env = {
      WANT_NET_CGO = "0";
      WANT_USER_CGO = "1";
      WANT_TZDATA = "0";
    };
  };
  mixed = {
    build.tags = [
      "unread_by_std"
      "netgo"
    ];
    env = {
      WANT_NET_CGO = "0";
      WANT_USER_CGO = "1";
      WANT_TZDATA = "0";
    };
  };
  skew = {
    goVersionSuffix = "-patched";
    build.tags = [ "unread_by_std" ];
    env = {
      WANT_NET_CGO = "1";
      WANT_USER_CGO = "1";
      WANT_TZDATA = "0";
    };
  };
}
