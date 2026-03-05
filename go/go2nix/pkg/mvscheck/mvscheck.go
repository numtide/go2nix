// Package mvscheck verifies that a project's go.mod is tidy with respect to
// its vendored module graph, using Go's own MVS implementation via
// `go mod graph`.
//
// A shared go2nix lockfile allows an untidy project go.mod (require foo@v1,
// MVS picks v2) to silently vendor foo@v1 if another project in the monorepo
// legitimately uses v1. This check catches that at build time.
//
// How it works: construct a minimal GOMODCACHE from the vendor tree's go.mod
// files, then run `go mod graph` with GOPROXY=off. For a tidy go.mod, every
// version the walk touches is in the cache. For an untidy one, Go fails naming
// the missing version.
package mvscheck

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/numtide/go2nix/pkg/lockfile"
)

// Check verifies that go.mod in dir is tidy with respect to vendor/.
// It constructs a fake GOMODCACHE from vendor/ and runs `go mod graph`.
func Check(dir string) error {
	goModPath := filepath.Join(dir, "go.mod")
	goModData, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("reading go.mod: %w", err)
	}

	cache, err := os.MkdirTemp("", "mvscheck-gomodcache-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)

	vendorDir := filepath.Join(dir, "vendor")
	if err := buildModCache(cache, vendorDir, goModData); err != nil {
		return fmt.Errorf("building GOMODCACHE from vendor tree: %w", err)
	}

	slog.Debug("running go mod graph", "dir", dir, "cache", cache)

	cmd := exec.Command("go", "mod", "graph")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+cache,
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOFLAGS=", // clear ambient -mod=vendor from the Nix build env
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"go.mod is not tidy with respect to the vendored module graph.\n"+
				"`go mod graph` failed looking for a version not in go.mod's require list:\n"+
				"\n%s\n"+
				"Run `go mod tidy` and regenerate the lockfile.",
			indent(strings.TrimSpace(string(out))),
		)
	}
	return nil
}

// buildModCache constructs a minimal GOMODCACHE from the vendor tree.
// For each required (path, version), it reads vendor/<path>/go.mod and
// writes cache/download/<path>/@v/<version>.mod + .info.
func buildModCache(cache, vendorDir string, goModData []byte) error {
	replaced := ReplacedPaths(goModData)
	for modPath, version := range RequireVersions(goModData) {
		if replaced[modPath] {
			continue
		}
		goModPath := filepath.Join(vendorDir, modPath, "go.mod")
		data, err := os.ReadFile(goModPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}

		dldir := filepath.Join(cache, "cache", "download", modPath, "@v")
		if err := os.MkdirAll(dldir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dldir, version+".mod"), data, 0o644); err != nil {
			return err
		}
		info := fmt.Sprintf(`{"Version":%q}`, version)
		if err := os.WriteFile(filepath.Join(dldir, version+".info"), []byte(info), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// RequireVersions parses go.mod bytes into module path -> required version.
func RequireVersions(goMod []byte) map[string]string {
	out := make(map[string]string)
	var inRequire bool
	for _, line := range bytes.Split(goMod, []byte("\n")) {
		l := stripComment(string(line))
		switch {
		case l == "require (":
			inRequire = true
		case l == ")" && inRequire:
			inRequire = false
		case inRequire:
			if p, v, ok := pathVersion(l); ok {
				out[p] = v
			}
		case strings.HasPrefix(l, "require "):
			if p, v, ok := pathVersion(strings.TrimPrefix(l, "require ")); ok {
				out[p] = v
			}
		}
	}
	return out
}

// ReplacedPaths returns the set of module paths on the LHS of replace
// directives — these bypass MVS and are exempt from the check.
func ReplacedPaths(goMod []byte) map[string]bool {
	out := make(map[string]bool)
	var inReplace bool
	for _, line := range bytes.Split(goMod, []byte("\n")) {
		l := stripComment(string(line))
		switch {
		case l == "replace (":
			inReplace = true
		case l == ")" && inReplace:
			inReplace = false
		case inReplace, strings.HasPrefix(l, "replace "):
			l = strings.TrimPrefix(l, "replace ")
			if i := strings.Index(l, "=>"); i > 0 {
				if fields := strings.Fields(l[:i]); len(fields) > 0 {
					out[fields[0]] = true
				}
			}
		}
	}
	return out
}

func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

func pathVersion(l string) (path, version string, ok bool) {
	fields := strings.Fields(l)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// Replace describes a go.mod replace directive.
type Replace struct {
	Path    string // target module path (or local directory)
	Version string // target version (empty for local replaces)
}

// ParseReplaces parses replace directives from go.mod, returning
// the full replace info (not just which paths are replaced).
func ParseReplaces(goMod []byte) map[string]Replace {
	out := make(map[string]Replace)
	var inReplace bool
	for _, line := range bytes.Split(goMod, []byte("\n")) {
		l := stripComment(string(line))
		switch {
		case l == "replace (":
			inReplace = true
		case l == ")" && inReplace:
			inReplace = false
		case inReplace, strings.HasPrefix(l, "replace "):
			l = strings.TrimPrefix(l, "replace ")
			if i := strings.Index(l, "=>"); i > 0 {
				lhs := strings.TrimSpace(l[:i])
				rhs := strings.TrimSpace(l[i+2:])
				lhsFields := strings.Fields(lhs)
				if len(lhsFields) == 0 {
					continue
				}
				modPath := lhsFields[0]
				rhsFields := strings.Fields(rhs)
				if len(rhsFields) == 0 {
					continue
				}
				r := Replace{Path: rhsFields[0]}
				if len(rhsFields) >= 2 {
					r.Version = rhsFields[1]
				}
				out[modPath] = r
			}
		}
	}
	return out
}

// CheckLockfile verifies that go.mod in dir is consistent with the go2nix
// lockfile. Every non-local-replaced module in go.mod's require block must
// have a matching module@version entry in the lockfile.
func CheckLockfile(dir string, lockfilePath string) error {
	goModData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return fmt.Errorf("reading go.mod: %w", err)
	}

	lf, err := lockfile.Read(lockfilePath)
	if err != nil {
		return err
	}

	requires := RequireVersions(goModData)
	replaces := ParseReplaces(goModData)

	var missing []string
	for modPath, reqVersion := range requires {
		repl, isReplaced := replaces[modPath]
		if isReplaced && repl.Version == "" {
			// Local replace (directory path, no version) — not in lockfile.
			continue
		}

		effectiveVersion := reqVersion
		if isReplaced && repl.Version != "" {
			effectiveVersion = repl.Version
		}

		key := modPath + "@" + effectiveVersion
		if _, ok := lf.Mod[key]; !ok {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf(
			"go.mod requires modules not found in lockfile %s:\n  %s\n\n"+
				"The lockfile is stale. Run `go mod tidy && gob generate` to update it.",
			lockfilePath,
			strings.Join(missing, "\n  "),
		)
	}

	return nil
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
