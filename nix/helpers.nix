# go2nix/nix/helpers.nix — shared utility functions.
rec {
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

  # Normalize a subPackages list: ensure all entries have "./" prefix so
  # they are interpreted as relative paths, not stdlib packages.
  # "." is passed through as-is.
  normalizeSubPackages = map (
    sp: if sp == "." || builtins.substring 0 2 sp == "./" then sp else "./${sp}"
  );

  # Parse go.mod text and return the list of local replace target paths
  # (as strings, relative to the go.mod's directory).
  #
  # `=>` appears only in `replace` directives (golang.org/x/mod/modfile/rule.go
  # parseReplace), so a single regex pass over the whole file finds every local
  # target regardless of inline vs block syntax. A replace target is local when
  # IsDirectoryPath(ns) is true — this regex covers the Unix `./` and `../`
  # prefix forms; bare `.`/`..` (no slash) and absolute `/` paths are valid per
  # IsDirectoryPath but intentionally out of scope here. [^[:space:]]* stops at
  # CR (CRLF-safe) and at the space before any trailing `// comment`.
  parseLocalReplaces =
    goModText:
    builtins.concatMap (x: if builtins.isList x then x else [ ]) (
      builtins.split "=>[[:space:]]+(\\.\\.?/[^[:space:]]*)" goModText
    );

  # Walk go.mod local replace directives transitively. Returns the list of
  # directory paths including the root module. genericClosure deduplicates
  # by key (the canonicalized path string), so diamond deps and cycles are
  # handled in C++ without a Nix-level visited set.
  #
  # Use this to construct a minimal source fileset for a monorepo module:
  # pass the module directory, get back just the directories the Go build
  # actually needs, avoiding full-repo source hashes.
  goModLocalReplaceDirs =
    dir:
    map (x: x.dir) (
      builtins.genericClosure {
        startSet = [
          {
            key = builtins.toString dir;
            inherit dir;
          }
        ];
        operator =
          { dir, ... }:
          let
            goMod = dir + "/go.mod";
            rels = if builtins.pathExists goMod then parseLocalReplaces (builtins.readFile goMod) else [ ];
          in
          map (
            r:
            let
              next = dir + "/${r}";
            in
            {
              key = builtins.toString next;
              dir = next;
            }
          ) rels;
      }
    );
}
