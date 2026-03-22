# go2nix/nix/helpers.nix — shared utility functions.
{
  # Make a string safe for use as a Nix derivation name.
  # Valid store-path characters: [a-zA-Z0-9+-._?=]
  # Go import paths may contain / ~ @, all of which are illegal.
  sanitizeName = builtins.replaceStrings [ "/" "~" "@" ] [ "-" "_" "_at_" ];

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
