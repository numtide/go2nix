package nixdrv

import "strings"

// SanitizeName converts a Go import path to a valid Nix store path name.
// Valid characters: [a-zA-Z0-9+-._?=]
// Replaces: / → -, + preserved, ~ → _, @ → _at_
// Any other illegal characters are replaced with _.
func SanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			b.WriteRune(c)
		case c == '+' || c == '-' || c == '.' || c == '_' || c == '?' || c == '=':
			b.WriteRune(c)
		case c == '/':
			b.WriteByte('-')
		case c == '@':
			b.WriteString("_at_")
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// PkgDrvName returns the derivation name for a package: gopkg-<sanitized>-<version>.
func PkgDrvName(importPath, version string) string {
	return "gopkg-" + SanitizeName(importPath) + "-" + version
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
