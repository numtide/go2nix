package buildinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

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
// moduleRoot is the path to the directory containing go.mod.
// Returns "" if the go.mod cannot be read or has no go directive.
func DefaultGODEBUG(moduleRoot string) string {
	goModPath := filepath.Join(moduleRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	mf, err := modfile.Parse(goModPath, data, nil)
	if err != nil || mf.Go == nil {
		return ""
	}

	goVersion := mf.Go.Version

	// Parse go version minor number.
	minor := parseGoMinor(goVersion)
	if minor < 0 {
		return ""
	}

	// cmd/go resolves the base version first (go directive, optionally
	// overridden by any `godebug default=goX.Y` directive), then layers
	// every explicit `godebug key=value` directive on top regardless of
	// its position relative to `default=`. Do the same: first scan for
	// `default=` to fix the base minor, then apply non-default directives.
	for _, gd := range mf.Godebug {
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

	// Apply explicit godebug directives from go.mod on top.
	for _, gd := range mf.Godebug {
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
