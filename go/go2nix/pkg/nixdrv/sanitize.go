package nixdrv

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// maxSanitizedLen caps the sanitized component so that the full derivation
// name (e.g. "gopkg-" + sanitized + "-" + 34-char pseudo-version) stays
// comfortably under Nix's 211-char store-name limit. Mirrored in
// rust/src/resolve.rs and nix/helpers.nix — keep all three in sync.
const maxSanitizedLen = 160

// SanitizeName converts a Go import path to a valid Nix store path name.
// Valid characters: [a-zA-Z0-9+-._?=]
// Replaces: / → -, + preserved, ~ → _, @ → _at_
// Any other illegal characters are replaced with _.
//
// If the sanitized result exceeds maxSanitizedLen it is truncated to a
// prefix plus an 8-hex-char sha256 of the original input, so very long
// import paths still yield valid, deterministic, collision-resistant names.
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
	san := b.String()
	if len(san) <= maxSanitizedLen {
		return san
	}
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:4])
	return san[:maxSanitizedLen-9] + "-" + h
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
