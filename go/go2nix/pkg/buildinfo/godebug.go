package buildinfo

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// A Godebug is a single key=value setting parsed from a //go:debug source
// directive (or, equivalently, a go.mod godebug line).
type Godebug struct {
	Key, Value string
}

var errNotGoDebug = errors.New("not //go:debug line")

// ParseGoDebug parses a //go:debug directive. It mirrors
// cmd/go/internal/load.ParseGoDebug minus the godebugs-table membership
// check (go2nix passes unknown keys through, like it does for go.mod
// godebug lines).
func ParseGoDebug(text string) (key, value string, err error) {
	if !strings.HasPrefix(text, "//go:debug") {
		return "", "", errNotGoDebug
	}
	i := strings.IndexAny(text, " \t")
	if i < 0 {
		if strings.TrimSpace(text) == "//go:debug" {
			return "", "", fmt.Errorf("missing key=value")
		}
		return "", "", errNotGoDebug
	}
	k, v, ok := strings.Cut(strings.TrimSpace(text[i:]), "=")
	if !ok {
		return "", "", fmt.Errorf("missing key=value")
	}
	if strings.ContainsAny(k, " \t,") || strings.ContainsAny(v, " \t,") {
		return "", "", fmt.Errorf("key or value contains space or comma")
	}
	return k, v, nil
}

// ParseSourceGodebugs collects //go:debug directives from the package at
// srcDir using go/build's directive extraction, the same mechanism cmd/go
// uses (Package.Directives). Directives that fail ParseGoDebug are skipped,
// matching cmd/go/internal/load.defaultGODEBUG.
func ParseSourceGodebugs(ctx *build.Context, srcDir string) []Godebug {
	if ctx == nil {
		c := build.Default
		ctx = &c
	}
	pkg, err := ctx.ImportDir(srcDir, 0)
	if err != nil && pkg == nil {
		return nil
	}
	var out []Godebug
	for _, d := range pkg.Directives {
		k, v, perr := ParseGoDebug(d.Text)
		if perr != nil {
			continue
		}
		out = append(out, Godebug{Key: k, Value: v})
	}
	return out
}

// godebugEntry mirrors internal/godebugs.Info for the subset we need.
// Only entries with Changed > 0 affect the default GODEBUG string.
type godebugEntry struct {
	Name    string
	Changed int    // minor version when default changed; 21 means Go 1.21
	Old     string // value that restores behavior prior to Changed
}

// godebugTable is copied from internal/godebugs/table.go.
// Only entries with Changed > 0 are included since those are the only ones
// that affect the default GODEBUG string.
//
// This table should be updated when upgrading the Go toolchain.
// Missing entries are benign — they only mean newer godebug compat
// defaults won't be applied for modules with older go directives.
var godebugTable = []godebugEntry{
	{Name: "asynctimerchan", Changed: 23, Old: "1"},
	{Name: "containermaxprocs", Changed: 25, Old: "0"},
	{Name: "cryptocustomrand", Changed: 26, Old: "1"},
	{Name: "decoratemappings", Changed: 25, Old: "0"},
	{Name: "gotestjsonbuildtext", Changed: 24, Old: "1"},
	{Name: "gotypesalias", Changed: 23, Old: "0"},
	{Name: "httpcookiemaxnum", Changed: 24, Old: "0"},
	{Name: "httplaxcontentlength", Changed: 22, Old: "1"},
	{Name: "httpmuxgo121", Changed: 22, Old: "1"},
	{Name: "httpservecontentkeepheaders", Changed: 23, Old: "1"},
	{Name: "multipathtcp", Changed: 24, Old: "0"},
	{Name: "netedns0", Changed: 19, Old: "0"},
	{Name: "panicnil", Changed: 21, Old: "1"},
	{Name: "randseednop", Changed: 24, Old: "0"},
	{Name: "rsa1024min", Changed: 24, Old: "0"},
	{Name: "tls10server", Changed: 22, Old: "1"},
	{Name: "tls3des", Changed: 23, Old: "1"},
	{Name: "tlsmlkem", Changed: 24, Old: "0"},
	{Name: "tlsrsakex", Changed: 22, Old: "1"},
	{Name: "tlssecpmlkem", Changed: 26, Old: "0"},
	{Name: "tlssha1", Changed: 25, Old: "1"},
	{Name: "tlsunsafeekm", Changed: 22, Old: "1"},
	{Name: "updatemaxprocs", Changed: 25, Old: "0"},
	{Name: "urlmaxqueryparams", Changed: 24, Old: "0"},
	{Name: "urlstrictcolons", Changed: 26, Old: "0"},
	{Name: "winreadlinkvolume", Changed: 23, Old: "0"},
	{Name: "winsymlink", Changed: 23, Old: "0"},
	{Name: "x509keypairleaf", Changed: 23, Old: "0"},
	{Name: "x509negativeserial", Changed: 23, Old: "1"},
	{Name: "x509rsacrt", Changed: 24, Old: "0"},
	{Name: "x509sha256skid", Changed: 25, Old: "0"},
	{Name: "x509usepolicies", Changed: 24, Old: "0"},
}

// DefaultGODEBUG computes the default GODEBUG string for a main package,
// matching cmd/go/internal/load.defaultGODEBUG.
//
// moduleRoot is the path to the directory containing go.mod. srcDirectives
// are //go:debug directives parsed from the main package's source files
// (see ParseSourceGodebugs); they are applied after go.mod's godebug lines
// so a source-file directive overrides a go.mod one for the same key,
// including default=. goFips140 is the GOFIPS140 build setting; when
// Fips140Enabled, fips140=on is added with lower precedence than go.mod
// and source directives (cmd/go/internal/load/godebug.go:68).
func DefaultGODEBUG(moduleRoot string, srcDirectives []Godebug, goFips140 string) string {
	goModPath := filepath.Join(moduleRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	mf, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return ""
	}

	// Mirrors gover.FromGoMod: a go.mod with no `go` directive is treated
	// as DefaultGoModVersion ("1.16"), the same fallback cmd/go's
	// MainModules.GoVersion uses. See cmd/go/internal/gover/version.go:64.
	goVersion := "1.16"
	if mf.Go != nil {
		goVersion = mf.Go.Version
	}

	minor := parseGoMinor(goVersion)
	if minor < 0 {
		return ""
	}

	// Merge go.mod godebug lines then source-file //go:debug directives, in
	// that order, so the last writer (source) wins on conflict — matching
	// cmd/go/internal/load.defaultGODEBUG which applies p.Internal.Build.
	// Directives after MainModules.Godebugs.
	directives := make([]Godebug, 0, len(mf.Godebug)+len(srcDirectives))
	for _, gd := range mf.Godebug {
		directives = append(directives, Godebug{Key: gd.Key, Value: gd.Value})
	}
	directives = append(directives, srcDirectives...)

	// cmd/go resolves the base version first (go directive, optionally
	// overridden by any `default=goX.Y` from go.mod or source), then layers
	// every explicit `key=value` directive on top regardless of its
	// position relative to `default=`. Do the same: first scan for
	// `default=` to fix the base minor, then apply non-default directives.
	for _, gd := range directives {
		if gd.Key != "default" {
			continue
		}
		v := strings.TrimPrefix(gd.Value, "go")
		if newMinor := parseGoMinor(v); newMinor >= 0 {
			minor = newMinor
		}
	}

	// Build defaults map: for each godebug entry where the effective
	// go version < Changed, use the old value.
	m := make(map[string]string)
	for _, entry := range godebugTable {
		if minor < entry.Changed {
			m[entry.Name] = entry.Old
		}
	}

	// GOFIPS140 != off implies fips140=on, with lower precedence than go.mod
	// and source directives (which are applied next), matching upstream.
	if Fips140Enabled(goFips140) {
		m["fips140"] = "on"
	}

	// Apply explicit non-default directives on top.
	for _, gd := range directives {
		if gd.Key == "default" {
			continue
		}
		m[gd.Key] = gd.Value
	}

	if len(m) == 0 {
		return ""
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%s=%s", k, m[k])
	}
	return b.String()
}

var goMinorRe = regexp.MustCompile(`^1\.(\d+)`)

// parseGoMinor extracts the minor version from a Go version string.
// "1.21" → 21, "1.21.3" → 21. Returns -1 on failure.
func parseGoMinor(v string) int {
	m := goMinorRe.FindStringSubmatch(v)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
