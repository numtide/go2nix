// Package lockfilegen produces go2nix lockfiles from Go projects.
package lockfilegen

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/numtide/go2nix/internal/gonix/nar"
	"github.com/numtide/go2nix/pkg/golist"
	"github.com/numtide/go2nix/pkg/lockfile"
	"golang.org/x/sync/errgroup"
)

// Generate creates a go2nix lockfile from the given project directories.
// mode selects the output format:
//   - "dag": lockfile with [mod] sections (package graph resolved at eval time by plugin)
//   - "dynamic": minimal lockfile with [mod] only
func Generate(dirs []string, output string, jobs int, mode string) error {
	cache, err := lockfile.Read(output)
	if err != nil {
		return fmt.Errorf("reading existing lockfile: %w", err)
	}
	slog.Info("cache loaded", "mods", len(cache.Mod))

	// Collect all modules from go.mod require blocks (with replaces applied).
	// This is the authoritative source: go.mod lists all modules across all
	// platforms, including indirect deps that go list -deps may omit on the
	// current host (e.g. platform-specific or test-only transitive deps).
	modKeys := make(map[string]bool)
	var mods []golist.ModInfo
	for _, dir := range dirs {
		slog.Info("collecting modules", "dir", dir)
		goModMods, err := golist.CollectGoModModules(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		for _, m := range goModMods {
			if !modKeys[m.Key] {
				modKeys[m.Key] = true
				mods = append(mods, m)
			}
		}
	}
	slog.Info("modules found", "count", len(mods))

	var toHash []golist.ModInfo
	resultMod := map[string]string{}
	resultReplace := map[string]string{}
	for _, m := range mods {
		if cached, ok := cache.Mod[m.Key]; ok && cache.Replace[m.Key] == m.Replaced() {
			resultMod[m.Key] = cached
			if r := m.Replaced(); r != "" {
				resultReplace[m.Key] = r
			}
		} else {
			toHash = append(toHash, m)
		}
	}
	slices.SortFunc(toHash, func(a, b golist.ModInfo) int { return strings.Compare(a.Key, b.Key) })
	slog.Info("hashing", "todo", len(toHash), "cached", len(resultMod))

	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(jobs)
	for _, m := range toHash {
		g.Go(func() error {
			slog.Debug("hashing module", "key", m.Key)
			h, err := modCacheHash(m.FetchPath, m.Version)
			if err != nil {
				return fmt.Errorf("hashing %s: %w", m.Key, err)
			}
			mu.Lock()
			resultMod[m.Key] = h
			if r := m.Replaced(); r != "" {
				resultReplace[m.Key] = r
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Omit replace if empty.
	if len(resultReplace) == 0 {
		resultReplace = nil
	}

	result := &lockfile.Lockfile{Mod: resultMod, Replace: resultReplace}
	slog.Info("writing lockfile", "mods", len(resultMod), "path", output)
	return result.Write(output, lockfile.Header)
}

func modCacheHash(fetchPath, version string) (string, error) {
	tmpdir, err := os.MkdirTemp("", "go2nix-hash-")
	if err != nil {
		return "", err
	}
	defer removeReadOnly(tmpdir)

	cmd := exec.Command("go", "mod", "download", fetchPath+"@"+version)
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+tmpdir,
		"GONOSUMCHECK=*",
		"GONOSUMDB=*",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go mod download %s@%s: %w\n%s", fetchPath, version, err, out)
	}

	return narHashPath(tmpdir)
}

func narHashPath(path string) (string, error) {
	h := sha256.New()
	if err := nar.DumpPath(h, path); err != nil {
		return "", fmt.Errorf("nar hash %s: %w", path, err)
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func removeReadOnly(dir string) {
	filepath.WalkDir(dir, func(path string, _ fs.DirEntry, _ error) error {
		os.Chmod(path, 0o755)
		return nil
	})
	os.RemoveAll(dir)
}
