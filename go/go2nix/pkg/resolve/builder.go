package resolve

import (
	"fmt"
	"strings"

	"github.com/numtide/go2nix/pkg/compile"
)

// shellQuote wraps a string in single quotes, escaping any embedded
// single quotes. This prevents shell injection in generated scripts.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// fodScript generates the bash builder script for a module FOD.
// Matches fetch-go-module.nix behavior.
func fodScript(goStorePath, fetchPath, version, cacertPath, netrcFile string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export HOME=\"$TMPDIR\"\n")
	b.WriteString("export GOMODCACHE=\"$out\"\n")
	b.WriteString("export GOSUMDB=off\n")
	b.WriteString("export GONOSUMCHECK='*'\n")
	if cacertPath != "" {
		fmt.Fprintf(&b, "export SSL_CERT_FILE=%s\n", shellQuote(cacertPath))
	}
	if netrcFile != "" {
		fmt.Fprintf(&b, "cp %s \"$HOME/.netrc\"\n", shellQuote(netrcFile))
		b.WriteString("chmod 600 \"$HOME/.netrc\"\n")
	}
	fmt.Fprintf(&b, "%s mod download %s\n", shellQuote(goStorePath), shellQuote(fetchPath+"@"+version))
	return b.String()
}

// compileScript generates the bash builder script for a package compilation.
// The script:
// 1. Writes importcfg and compile manifest from environment variables
// 2. Calls go2nix compile-package --manifest (same interface as default mode)
func compileScript(go2nixBin string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export HOME=\"$TMPDIR\"\n")
	b.WriteString("mkdir -p \"$out\"\n\n")

	// Write importcfg from env var (placeholders resolved by Nix at build time).
	// Use absolute path since compile-package changes CWD to srcdir.
	b.WriteString("printf '%s\\n' \"$importcfg_entries\" > \"$NIX_BUILD_TOP/importcfg\"\n\n")

	// Write compile manifest from env var (JSON generated at derivation creation time).
	// Replace @@IMPORTCFG@@ placeholder with the actual importcfg path so that
	// the JSON consumed by compile-package contains the resolved path.
	fmt.Fprintf(&b, "printf '%%s\\n' \"${compileManifestJSON//%s/$NIX_BUILD_TOP/importcfg}\" > \"$NIX_BUILD_TOP/compile-manifest.json\"\n\n", compile.ImportcfgPlaceholder)

	// Source directory: modSrc/relDir for third-party, srcRoot/relDir for local
	b.WriteString("srcdir=\"$modSrc/$relDir\"\n\n")

	// Compile using go2nix compile-package with manifest
	fmt.Fprintf(&b, "%s compile-package", shellQuote(go2nixBin))
	b.WriteString(" \\\n  --manifest \"$NIX_BUILD_TOP/compile-manifest.json\"")
	b.WriteString(" \\\n  --import-path \"$importPath\"")
	b.WriteString(" \\\n  --src-dir \"$srcdir\"")
	b.WriteString(" \\\n  --output \"$out/pkg.a\"")
	b.WriteString(" \\\n  --trim-path \"$NIX_BUILD_TOP\"")
	// Override -p flag for main packages (pflag env var)
	b.WriteString(" \\\n  ${pflag:+--p \"$pflag\"}")
	// Go language version for -lang flag (from module's go.mod)
	b.WriteString(" \\\n  ${goVersion:+--go-version \"$goVersion\"}")
	b.WriteString("\n")
	return b.String()
}

// linkScript generates the bash builder script for linking a binary.
// buildMode should be "pie" or "exe", matching cmd/go's default for
// the target platform (see compile.DefaultBuildMode).
func linkScript(goStorePath, pname, buildMode string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export HOME=\"$TMPDIR\"\n")
	b.WriteString("mkdir -p \"$out/bin\"\n\n")

	// Write importcfg for all transitive deps
	b.WriteString("printf '%s\\n' \"$importcfg_entries\" > \"$NIX_BUILD_TOP/importcfg\"\n\n")

	// Set GOROOT so the linker embeds it as runtime.defaultGOROOT,
	// enabling runtime.GOROOT() in the resulting binary.
	b.WriteString("export GOROOT=\"$goroot\"\n\n")

	// Link binary. pname is validated by Nix (store path component) so
	// it cannot contain shell metacharacters, but we quote defensively.
	fmt.Fprintf(&b, "%s tool link -o \"$out/bin/\"%s", shellQuote(goStorePath), shellQuote(pname))
	b.WriteString(" \\\n  -importcfg \"$NIX_BUILD_TOP/importcfg\"")
	b.WriteString(" \\\n  -buildid=")
	fmt.Fprintf(&b, " \\\n  -buildmode=%s", shellQuote(buildMode))
	// External linker for cgo packages — uses CC or CXX depending on
	// whether C++ files are present, matching Go's setextld (gc.go).
	b.WriteString(" \\\n  ${extld:+-extld \"$extld\" -linkmode external}")
	// Sanitizer flags (-race, -msan, -asan) propagated from gcflags
	b.WriteString(" \\\n  ${sanitizerLinkFlags:+$sanitizerLinkFlags}")
	// GODEBUG default from go.mod's go directive (gc.go:624-626)
	b.WriteString(" \\\n  ${godebugDefault:+-X=runtime.godebugDefault=$godebugDefault}")
	// ldflags passed via env
	b.WriteString(" \\\n  ${ldflags:+$ldflags}")
	b.WriteString(" \\\n  \"$mainPkg/pkg.a\"\n")
	return b.String()
}

// collectScript generates the bash builder script for a collector derivation
// that merges multiple link outputs into one.
func collectScript(placeholders []string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("mkdir -p \"$out/bin\"\n")
	for _, ph := range placeholders {
		// Placeholders are Nix store path hashes (e.g., "/1abc..."), safe
		// by construction, but we quote defensively.
		fmt.Fprintf(&b, "cp %s/bin/* \"$out/bin/\"\n", shellQuote(ph))
	}
	return b.String()
}
