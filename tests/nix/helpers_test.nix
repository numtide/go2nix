# tests/nix/helpers_test.nix — unit tests for nix/helpers.nix
#
# Run: nix eval -f tests/nix/helpers_test.nix
# Returns true on success, throws on failure.
let
  helpers = import ../../nix/helpers.nix;
  inherit (helpers)
    modKeyPath
    sanitizeName
    removePrefix
    escapeModPath
    ;

  assertEq =
    name: actual: expected:
    if actual == expected then
      true
    else
      builtins.throw "${name}: got \"${actual}\", want \"${expected}\"";
in

# --- modKeyPath ---
assert assertEq "modKeyPath basic" (modKeyPath "github.com/foo/bar@v1.2.3" "v1.2.3")
  "github.com/foo/bar";

assert assertEq "modKeyPath pre-release"
  (modKeyPath "golang.org/x/net@v0.0.0-20231215" "v0.0.0-20231215")
  "golang.org/x/net";

assert assertEq "modKeyPath long path" (modKeyPath "github.com/a/b/c/d@v2.0.0" "v2.0.0")
  "github.com/a/b/c/d";

assert assertEq "modKeyPath v0" (modKeyPath "example.com/mod@v0.1.0" "v0.1.0") "example.com/mod";

# --- sanitizeName ---
assert assertEq "sanitizeName slashes" (sanitizeName "github.com/foo/bar") "github.com-foo-bar";

assert assertEq "sanitizeName plus" (sanitizeName "google.golang.org/grpc+extra")
  "google.golang.org-grpc_extra";

assert assertEq "sanitizeName both" (sanitizeName "github.com/a+b/c+d") "github.com-a_b-c_d";

assert assertEq "sanitizeName no change" (sanitizeName "simple-name") "simple-name";

# --- removePrefix ---
assert assertEq "removePrefix basic" (removePrefix "github.com/foo/" "github.com/foo/bar/baz")
  "bar/baz";

assert assertEq "removePrefix whole string" (removePrefix "abc" "abc") "";

assert assertEq "removePrefix empty prefix" (removePrefix "" "hello") "hello";

# --- escapeModPath ---
assert assertEq "escapeModPath uppercase" (escapeModPath "github.com/Azure/go-autorest")
  "github.com/!azure/go-autorest";

assert assertEq "escapeModPath multiple uppercase" (escapeModPath "github.com/BurntSushi/toml")
  "github.com/!burnt!sushi/toml";

assert assertEq "escapeModPath no uppercase" (escapeModPath "github.com/foo/bar")
  "github.com/foo/bar";

assert assertEq "escapeModPath all caps" (escapeModPath "ABC") "!a!b!c";

assert assertEq "escapeModPath mixed" (escapeModPath "github.com/FiloSottile/yubikey-agent")
  "github.com/!filo!sottile/yubikey-agent";

true
