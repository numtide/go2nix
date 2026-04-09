// Package mvscheck verifies that a project's go.mod is consistent with its
// go2nix lockfile. Every non-local-replaced module in go.mod's require block
// must have a matching module@version entry in the lockfile.
package mvscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/numtide/go2nix/pkg/lockfile"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

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

	// Key by (Old.Path, Old.Version): a replace with Old.Version != "" applies
	// only when the module resolves to exactly that version. The wildcard form
	// (Old.Version == "") applies to any version. Mirrors modfile semantics.
	replaces := make(map[module.Version]*modfile.Replace, len(mf.Replace))
	for _, r := range mf.Replace {
		replaces[r.Old] = r
	}

	var missing []string
	for _, req := range mf.Require {
		modPath := req.Mod.Path
		version := req.Mod.Version

		r, ok := replaces[req.Mod]
		if !ok {
			r, ok = replaces[module.Version{Path: modPath}]
		}
		if ok {
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
				"the lockfile is stale; run `go mod tidy && go2nix generate` to update it",
			lockfilePath,
			strings.Join(missing, "\n  "),
		)
	}

	return nil
}
