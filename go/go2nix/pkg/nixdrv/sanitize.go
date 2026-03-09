package nixdrv

import (
	"strings"
	"unicode"
)

// SanitizeName converts a Go import path to a valid Nix derivation name component.
// Matches helpers.nix sanitizeName: replace / → -, + → _
// Dots and @ are valid in Nix derivation names and are preserved.
func SanitizeName(s string) string {
	r := strings.NewReplacer("/", "-", "+", "_")
	return r.Replace(s)
}

// PkgDrvName returns the derivation name for a package: gopkg-<sanitized>.
func PkgDrvName(importPath string) string {
	return "gopkg-" + SanitizeName(importPath)
}

// ModDrvName returns the derivation name for a module FOD: gomod-<sanitized>.
func ModDrvName(modKey string) string {
	return "gomod-" + SanitizeName(modKey)
}

// LinkDrvName returns the derivation name for a link: golink-<sanitized>.
func LinkDrvName(pname string) string {
	return "golink-" + SanitizeName(pname)
}

// CollectDrvName returns the derivation name for a collector: gocollect-<sanitized>.
func CollectDrvName(pname string) string {
	return "gocollect-" + SanitizeName(pname)
}

// EscapeModPath escapes a Go module path for use in GOMODCACHE paths.
// Matches golang.org/x/mod/module.EscapePath(): uppercase letters become !lowercase.
func EscapeModPath(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
