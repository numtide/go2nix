# go2nix/nix/helpers.nix — shared utility functions.
{
  # Strip @version suffix from a module key to get the module path.
  # Cheaper than regex: version is already a field, so just truncate.
  modKeyPath = key: version:
    builtins.substring 0 (builtins.stringLength key - builtins.stringLength version - 1) key;

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
