# go2nix/nix/importcfg.nix — generate importcfg shell snippet.
#
# Produces shell commands that build an importcfg file starting from the
# stdlib importcfg and adding entries from dep derivations.
# Each dep is expected to have $out/<importpath>.a
{
  stdlib,
  deps, # list of package derivations
}:
''
  cat "${stdlib}/importcfg" > importcfg
  ${builtins.concatStringsSep "\n" (
    map (dep: ''
      find "${dep}" -name '*.a' | while read -r pkgp; do
        relpath="''${pkgp#"${dep}/"}"
        pkgname="''${relpath%.a}"
        echo "packagefile $pkgname=$pkgp"
      done >> importcfg
    '') deps
  )}

  # Detect import path conflicts.
  _dupes=$(awk -F'[= ]' '/^packagefile /{print $2}' importcfg | sort | uniq -d)
  if [ -n "$_dupes" ]; then
    echo "ERROR: duplicate import paths in importcfg:" >&2
    echo "$_dupes" >&2
    exit 1
  fi
''
