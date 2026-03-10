// Command mvscheck verifies that the project's go.mod is tidy with respect to
// the vendored module graph — using Go's own MVS implementation.
//
// Why this exists: a shared go2nix lockfile allows an untidy project go.mod
// (require foo@v1, MVS picks v2) to silently vendor foo@v1 if another project
// in the monorepo legitimately uses v1. The CLI's tidiness check catches this
// at generate time, but go.mod can be edited afterward. The Nix eval-time
// check only catches missing entries, not wrong-but-present ones.
//
// How it works: a tidy go.mod's `require` block (direct + `// indirect`
// entries) is exactly the set of MVS-selected versions — that's what
// `go mod tidy` writes. We construct a minimal GOMODCACHE containing .mod +
// .info files for exactly those versions (read from the vendor tree, which in
// gomod2nix-style vendoring preserves go.mod files). Then we run
// `go mod graph`, which walks the full module require graph using the .mod
// files. For a tidy go.mod, every version the walk touches is already in the
// cache. For an untidy go.mod, the walk reaches a version NOT in our cache
// (because a transitive dependency requires something higher than go.mod
// claims), and Go fails with "module lookup disabled by GOPROXY=off" naming
// the missing version.
//
// This uses Go's MVS implementation; we implement nothing more than a directory
// walk and a go.mod parser.
//
// Usage: run from the directory containing go.mod and vendor/. No arguments.
// Exits non-zero on failure.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := check(os.TempDir()); err != nil {
		fmt.Fprintln(os.Stderr, "mvscheck:", err)
		os.Exit(1)
	}
}

func check(tmpRoot string) error {
	goModData, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}

	cache, err := os.MkdirTemp(tmpRoot, "mvscheck-gomodcache-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)

	if err := buildModCache(cache, goModData); err != nil {
		return fmt.Errorf("building GOMODCACHE from vendor tree: %w", err)
	}

	// `go mod graph` walks the full module require graph, consulting .mod
	// files from GOMODCACHE. If go.mod is tidy, the require list covers
	// every version the walk needs (by definition of tidy). If not, the
	// walk reaches a version we didn't populate, and Go errors with the
	// missing version in stderr.
	cmd := exec.Command("go", "mod", "graph")
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+cache,
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOFLAGS=", // clear ambient -mod=vendor from the Nix build env
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		// The stderr from `go mod graph` already names the missing version
		// (e.g., "golang.org/x/mod@v0.23.0: module lookup disabled by
		// GOPROXY=off"). That's the version MVS wanted but our require list
		// didn't have — the exact diagnostic we need.
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

// buildModCache constructs a minimal GOMODCACHE from the project's require
// list. For each required (path, version), it reads vendor/<path>/go.mod and
// writes cache/download/<path>/@v/<version>.mod + .info. This is exactly
// what `go mod graph` needs.
func buildModCache(cache string, goModData []byte) error {
	replaced := replacedPaths(goModData)
	for modPath, version := range requireVersions(goModData) {
		if replaced[modPath] {
			// Replaces decouple require version from what's actually used;
			// they're out of MVS's purview. Skip so `go mod graph` doesn't
			// try to resolve them.
			continue
		}
		goModPath := filepath.Join("vendor", modPath, "go.mod")
		data, err := os.ReadFile(goModPath)
		if os.IsNotExist(err) {
			// Module in require but not vendored — local replace (covered
			// above), or a module with no Go source (go.sum-only). Either
			// way `go mod graph` won't need to walk into it.
			continue
		}
		if err != nil {
			return err
		}

		// Module path encoding (uppercase -> !lowercase) per
		// golang.org/x/mod/module.EscapePath would be needed for mixed-case
		// module paths. The Go proxy protocol forbids those, and none exist
		// in practice; if one appears, `go mod graph` will name it clearly.
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

// requireVersions parses go.mod bytes → module path -> required version.
// Hand-rolled because mvscheck is compiled at Nix eval time from a single
// self-contained .go file with no dependencies (see mkInternalPkg in the
// builder's default.nix).
func requireVersions(goMod []byte) map[string]string {
	out := make(map[string]string)
	var inRequire bool
	for line := range bytes.SplitSeq(goMod, []byte("\n")) {
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

// replacedPaths returns the set of module paths on the LHS of replace
// directives — these bypass MVS and are exempt from the check.
func replacedPaths(goMod []byte) map[string]bool {
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

func indent(s string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
