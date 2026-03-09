package resolve

import (
	"fmt"
	"strings"
)

// fodScript generates the bash builder script for a module FOD.
// Matches fetch-go-module.nix behavior.
func fodScript(goStorePath, fetchPath, version, cacertPath string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export HOME=$TMPDIR\n")
	b.WriteString("export GOMODCACHE=$out\n")
	b.WriteString("export GOSUMDB=off\n")
	b.WriteString("export GONOSUMCHECK='*'\n")
	if cacertPath != "" {
		fmt.Fprintf(&b, "export SSL_CERT_FILE=%s\n", cacertPath)
	}
	fmt.Fprintf(&b, "%s mod download \"%s@%s\"\n", goStorePath, fetchPath, version)
	return b.String()
}

// compileScript generates the bash builder script for a package compilation.
// The script:
// 1. Writes importcfg from the environment variable
// 2. Calls go2nix compile-package
func compileScript(go2nixBin string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("mkdir -p $out\n\n")

	// Write importcfg from env var (placeholders resolved by Nix at build time)
	b.WriteString("printf '%s\\n' \"$importcfg_entries\" > importcfg\n\n")

	// Source directory: modSrc/relDir for third-party, srcRoot/relDir for local
	b.WriteString("srcdir=\"$modSrc/$relDir\"\n\n")

	// Compile using go2nix compile-package
	fmt.Fprintf(&b, "%s compile-package", go2nixBin)
	b.WriteString(" \\\n  --import-path \"$importPath\"")
	b.WriteString(" \\\n  --import-cfg importcfg")
	b.WriteString(" \\\n  --src-dir \"$srcdir\"")
	b.WriteString(" \\\n  --output \"$out/pkg.a\"")
	b.WriteString(" \\\n  --trim-path \"$NIX_BUILD_TOP\"")
	// Only pass tags if set
	b.WriteString("\n")
	return b.String()
}

// linkScript generates the bash builder script for linking a binary.
func linkScript(goStorePath, pname string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("mkdir -p $out/bin\n\n")

	// Write importcfg for all transitive deps
	b.WriteString("printf '%s\\n' \"$importcfg_entries\" > importcfg\n\n")

	// Link binary
	fmt.Fprintf(&b, "%s tool link -o $out/bin/%s", goStorePath, pname)
	b.WriteString(" \\\n  -importcfg importcfg")
	b.WriteString(" \\\n  -buildmode=exe")
	// ldflags passed via env
	b.WriteString(" \\\n  ${ldflags:+$ldflags}")
	b.WriteString(" \\\n  $mainPkg/pkg.a\n")
	return b.String()
}

// collectScript generates the bash builder script for a collector derivation
// that merges multiple link outputs into one.
func collectScript(placeholders []string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("mkdir -p $out/bin\n")
	for _, ph := range placeholders {
		fmt.Fprintf(&b, "cp %s/bin/* $out/bin/\n", ph)
	}
	return b.String()
}
