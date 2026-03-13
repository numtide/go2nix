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
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/numtide/go2nix/pkg/lockfile"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Check verifies that go.mod in dir is tidy with respect to vendor/.
// It constructs a fake GOMODCACHE from vendor/ and runs `go mod graph`.
func Check(dir string) error {
	goModPath := filepath.Join(dir, "go.mod")
	goModData, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("reading go.mod: %w", err)
	}
	mf, err := modfile.Parse("go.mod", goModData, nil)
	if err != nil {
		return fmt.Errorf("parsing go.mod: %w", err)
	}

	cache, err := os.MkdirTemp("", "mvscheck-gomodcache-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)

	vendorDir := filepath.Join(dir, "vendor")
	if err := buildModCache(cache, vendorDir, mf); err != nil {
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
func buildModCache(cache, vendorDir string, mf *modfile.File) error {
	replaced := make(map[string]bool, len(mf.Replace))
	for _, r := range mf.Replace {
		replaced[r.Old.Path] = true
	}

	for _, req := range mf.Require {
		modPath := req.Mod.Path
		version := req.Mod.Version
		if replaced[modPath] {
			continue
		}

		goModPath := filepath.Join(vendorDir, modPath, "go.mod")
		data, err := os.ReadFile(goModPath)
		if err != nil {
			return fmt.Errorf("vendor tree missing go.mod for required module %s@%s: %w", modPath, version, err)
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

// CheckLockfile verifies that go.mod in dir is consistent with the go2nix
// lockfile. Every non-local-replaced module in go.mod's require block must
// have a matching module@version entry in the lockfile.
func CheckLockfile(dir string, lockfilePath string) error {
	goModData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return fmt.Errorf("reading go.mod: %w", err)
	}
	mf, err := modfile.Parse("go.mod", goModData, nil)
	if err != nil {
		return fmt.Errorf("parsing go.mod: %w", err)
	}

	lf, err := lockfile.Read(lockfilePath)
	if err != nil {
		return err
	}

	replaces := make(map[string]*modfile.Replace, len(mf.Replace))
	for _, r := range mf.Replace {
		replaces[r.Old.Path] = r
	}

	var missing []string
	for _, req := range mf.Require {
		modPath := req.Mod.Path
		version := req.Mod.Version

		if r, ok := replaces[modPath]; ok {
			if r.New.Version == "" {
				// Local replace (directory path, no version) — not in lockfile.
				continue
			}
			version = r.New.Version
		}

		key := module.Version{Path: modPath, Version: version}.String()
		if _, ok := lf.Mod[key]; !ok {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf(
			"go.mod requires modules not found in lockfile %s:\n  %s\n\n"+
				"The lockfile is stale. Run `go mod tidy && go2nix generate` to update it.",
			lockfilePath,
			strings.Join(missing, "\n  "),
		)
	}

	return nil
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
