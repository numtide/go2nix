package resolve

import (
	"fmt"
	"strings"
)

// fodScript generates the bash builder script for a module FOD.
// Matches fetch-go-module.nix behavior.
func fodScript(goStorePath, fetchPath, version, cacertPath, netrcFile string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export HOME=$TMPDIR\n")
	b.WriteString("export GOMODCACHE=$out\n")
	b.WriteString("export GOSUMDB=off\n")
	b.WriteString("export GONOSUMCHECK='*'\n")
	if cacertPath != "" {
		fmt.Fprintf(&b, "export SSL_CERT_FILE=%s\n", cacertPath)
	}
	if netrcFile != "" {
		fmt.Fprintf(&b, "cp %s $HOME/.netrc\n", netrcFile)
		b.WriteString("chmod 600 $HOME/.netrc\n")
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
	b.WriteString("export HOME=$TMPDIR\n")
	b.WriteString("mkdir -p $out\n\n")

	// Write importcfg from env var (placeholders resolved by Nix at build time).
	// Use absolute path since compile-package changes CWD to srcdir.
	b.WriteString("printf '%s\\n' \"$importcfg_entries\" > $NIX_BUILD_TOP/importcfg\n\n")

	// Source directory: modSrc/relDir for third-party, srcRoot/relDir for local
	b.WriteString("srcdir=\"$modSrc/$relDir\"\n\n")

	// Compile using go2nix compile-package
	fmt.Fprintf(&b, "%s compile-package", go2nixBin)
	b.WriteString(" \\\n  --import-path \"$importPath\"")
	b.WriteString(" \\\n  --import-cfg $NIX_BUILD_TOP/importcfg")
	b.WriteString(" \\\n  --src-dir \"$srcdir\"")
	b.WriteString(" \\\n  --output \"$out/pkg.a\"")
	b.WriteString(" \\\n  --trim-path \"$NIX_BUILD_TOP\"")
	// Override -p flag for main packages (pflag env var)
	b.WriteString(" \\\n  ${pflag:+--p \"$pflag\"}")
	// Only pass tags if set (env var set by createPackageDrv)
	b.WriteString(" \\\n  ${tags:+--tags \"$tags\"}")
	// Only pass gcflags if set
	b.WriteString(" \\\n  ${gcflags:+--gc-flags \"$gcflags\"}")
	b.WriteString("\n")
	return b.String()
}

// linkScript generates the bash builder script for linking a binary.
// buildMode should be "pie" or "exe", matching cmd/go's default for
// the target platform (see compile.DefaultBuildMode).
func linkScript(goStorePath, pname, buildMode string) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export HOME=$TMPDIR\n")
	b.WriteString("mkdir -p $out/bin\n\n")

	// Write importcfg for all transitive deps
	b.WriteString("printf '%s\\n' \"$importcfg_entries\" > $NIX_BUILD_TOP/importcfg\n\n")

	// Link binary
	fmt.Fprintf(&b, "%s tool link -o \"$out/bin/%s\"", goStorePath, pname)
	b.WriteString(" \\\n  -importcfg $NIX_BUILD_TOP/importcfg")
	fmt.Fprintf(&b, " \\\n  -buildmode=%s", buildMode)
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
