# go2nix/nix/helpers.nix — shared utility functions.
{
  # Parse a module key like "github.com/foo/bar@v1.2.3" into { path, version }.
  # Uses match instead of split: avoids regex iterator allocation and
  # the 2*m+1 intermediate list that split produces (see primops.cc:4820).
  parseModKey =
    key:
    let
      m = builtins.match "(.+)@(.+)" key;
    in
    {
      path = builtins.elemAt m 0;
      version = builtins.elemAt m 1;
    };

  # Make a string safe for use as a Nix derivation name.
  sanitizeName = builtins.replaceStrings [ "/" "+" ] [ "-" "_" ];

  # Remove a prefix from a string. Assumes prefix is actually a prefix.
  removePrefix =
    prefix: str: builtins.substring (builtins.stringLength prefix) (builtins.stringLength str) str;

  # Go module path case-escaping: uppercase letters become "!" + lowercase.
  # This matches the GOMODCACHE filesystem layout.
  # See: golang.org/x/mod/module.EscapePath()
  escapeModPath =
    builtins.replaceStrings
      [
        "A"
        "B"
        "C"
        "D"
        "E"
        "F"
        "G"
        "H"
        "I"
        "J"
        "K"
        "L"
        "M"
        "N"
        "O"
        "P"
        "Q"
        "R"
        "S"
        "T"
        "U"
        "V"
        "W"
        "X"
        "Y"
        "Z"
      ]
      [
        "!a"
        "!b"
        "!c"
        "!d"
        "!e"
        "!f"
        "!g"
        "!h"
        "!i"
        "!j"
        "!k"
        "!l"
        "!m"
        "!n"
        "!o"
        "!p"
        "!q"
        "!r"
        "!s"
        "!t"
        "!u"
        "!v"
        "!w"
        "!x"
        "!y"
        "!z"
      ];
}
