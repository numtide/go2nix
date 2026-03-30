# tests/nix/helpers_test.nix — unit tests for nix/helpers.nix
#
# Run: nix eval -f tests/nix/helpers_test.nix
# Returns true on success, throws on failure.
let
  helpers = import ../../nix/helpers.nix;
  inherit (helpers)
    sanitizeName
    removePrefix
    escapeModPath
    parseLocalReplaces
    goModLocalReplaceDirs
    ;

  assertEq =
    name: actual: expected:
    if actual == expected then
      true
    else
      builtins.throw "${name}: got \"${actual}\", want \"${expected}\"";
in

# --- sanitizeName ---
assert assertEq "sanitizeName slashes" (sanitizeName "github.com/foo/bar") "github.com-foo-bar";

assert assertEq "sanitizeName plus preserved" (sanitizeName "google.golang.org/grpc+extra")
  "google.golang.org-grpc+extra";

assert assertEq "sanitizeName slash with plus" (sanitizeName "github.com/a+b/c+d")
  "github.com-a+b-c+d";

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

# --- parseLocalReplaces ---
let
  assertListEq =
    name: actual: expected:
    if actual == expected then
      true
    else
      builtins.throw "${name}: got ${builtins.toJSON actual}, want ${builtins.toJSON expected}";
  sort = builtins.sort builtins.lessThan;
in

assert assertListEq "parseLocalReplaces inline"
  (parseLocalReplaces ''
    module example.com/m
    go 1.25
    replace github.com/a/b => ../a-b
    replace github.com/c/d => github.com/c/d v1.2.3
    replace github.com/e/f => ./local
  '')
  [
    "../a-b"
    "./local"
  ];

assert assertListEq "parseLocalReplaces block"
  (parseLocalReplaces ''
    module example.com/m
    replace (
    	github.com/a/b => ../a-b
    	github.com/c/d => github.com/c/d v1.2.3
    	github.com/e/f => ../e-f
    )
  '')
  [
    "../a-b"
    "../e-f"
  ];

assert assertListEq "parseLocalReplaces mixed inline+block"
  (parseLocalReplaces ''
    replace github.com/x => ../x
    replace (
    	github.com/y => ../y
    )
    replace github.com/z => github.com/z v0.0.1
  '')
  [
    "../x"
    "../y"
  ];

assert assertListEq "parseLocalReplaces versioned LHS" (parseLocalReplaces ''
  replace github.com/a/b v1.0.0 => ../a-b
'') [ "../a-b" ];

assert assertListEq "parseLocalReplaces empty" (parseLocalReplaces ''
  module example.com/m
  go 1.25
'') [ ];

assert assertListEq "parseLocalReplaces CRLF"
  (parseLocalReplaces "replace github.com/a => ../a\r\nreplace github.com/b => ../b\r\n")
  [
    "../a"
    "../b"
  ];

# --- goModLocalReplaceDirs ---
# torture-project/app-partial has block replaces pointing to 5 internal/* dirs,
# each of which (transitively) replace => ../common.
let
  torture = ../fixtures/torture-project;
  dirs = goModLocalReplaceDirs (torture + "/app-partial");
  dirStrs = sort (map (d: removePrefix (builtins.toString torture + "/") (builtins.toString d)) dirs);
in
assert assertListEq "goModLocalReplaceDirs app-partial transitive" dirStrs [
  "app-partial"
  "internal/aws"
  "internal/common"
  "internal/conflict-a"
  "internal/crypto"
  "internal/db"
];

# Diamond: app-partial → {aws, crypto, db, ...} → common. Visited-set must dedupe.
let
  torture = ../fixtures/torture-project;
  dirs = goModLocalReplaceDirs (torture + "/internal/aws");
  dirStrs = sort (map (d: removePrefix (builtins.toString torture + "/") (builtins.toString d)) dirs);
in
assert assertListEq "goModLocalReplaceDirs single hop" dirStrs [
  "internal/aws"
  "internal/common"
];

true
