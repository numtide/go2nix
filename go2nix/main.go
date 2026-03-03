// Command go2nix generates a Nix lockfile mapping Go module versions to NAR
// hashes, for consumption by the Nix builder in ./builder.
//
// Unlike gomod2nix (from which the builder is forked), the lockfile is keyed
// by `module@version` rather than bare module path. This has two consequences:
//
//   - A single lockfile can be shared across N projects in a monorepo: each
//     project's build filters the lockfile against its own go.mod.
//   - A stale lockfile is a build failure, not a silent wrong-dependency:
//     if go.mod requires foo@v1.1 but the lockfile only has foo@v1.0, the
//     filter finds nothing and the build fails.
//
// See README.md for design rationale and usage.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/modfile"
	"golang.org/x/sync/errgroup"
)

// --- Lockfile schema ---------------------------------------------------------

// Entry is one module in the lockfile.
// TOML key is "module_path@version"; Version is repeated in the value so the
// Nix builder can recover the bare module path by stripping the version suffix
// (rather than regex-parsing the key).
type Entry struct {
	Version string `toml:"version"`
	Hash    string `toml:"hash"` // SRI NAR hash, e.g. "sha256-..."
	// Replaced is set when go.mod has a remote `replace origPath => newPath vX`;
	// the key uses origPath (so go.mod's require can find it) but Nix must
	// fetch newPath. Not set for local (filesystem) replaces — those are
	// handled entirely by the Nix builder via symlinks.
	Replaced string `toml:"replaced,omitempty"`
}

type lockfile struct {
	Mod map[string]Entry `toml:"mod"`
}

// readLockfile returns the current lockfile contents, or an empty lockfile if
// the file doesn't exist.
func readLockfile(path string) (*lockfile, error) {
	lf := &lockfile{Mod: map[string]Entry{}}
	data, err := os.ReadFile(path) //nolint:gosec // path is user-provided by design
	if errors.Is(err, os.ErrNotExist) {
		return lf, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := toml.Decode(string(data), lf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return lf, nil
}

// write encodes the lockfile to disk with a leading header.
// BurntSushi/toml sorts map keys, so output is deterministic.
func (lf *lockfile) write(path, header string) error {
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := toml.NewEncoder(&buf).Encode(lf); err != nil {
		return err
	}
	// 0644: lockfile is committed to git and read by nix at build time.
	return os.WriteFile(path, buf.Bytes(), 0o644) //nolint:gosec
}

// --- Module collection -------------------------------------------------------

// mod describes one module to include in the lockfile.
type mod struct {
	// key is what we record in the lockfile and what the Nix builder's filter
	// looks for: "origPath@version" where origPath is the path as it appears
	// in the consumer's `require` directive, and version is what `go mod
	// download` actually resolved (which for a replaced module is the
	// replacement's version).
	key string
	// fetchPath is what `go mod download` and nix must actually fetch.
	// Differs from the key's path component only for remote replaces.
	fetchPath string
	version   string
	dir       string // local cache dir populated by `go mod download`
}

// replaced returns the value for Entry.Replaced: the fetchPath if it differs
// from the key's path component, else empty.
func (m mod) replaced() string {
	if origPath := strings.TrimSuffix(m.key, "@"+m.version); origPath != m.fetchPath {
		return m.fetchPath
	}
	return ""
}

// goModDownload matches one JSON record from `go mod download -json`.
// See `go help mod download`.
type goModDownload struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
	Error   string `json:"Error"`
}

// collectModules runs `go mod download -json` in dir and returns every
// non-local module it needs.
//
// TIDINESS CHECK: collectModules errors if go.mod is not tidy — that is, if
// the `require` directive's versions don't match the MVS-resolved versions
// from `go mod download`. Without this, the shared-lockfile filter could
// match a stale version put there by another project (see README §Tidiness).
// The Nix build has its own backstop (mvscheck in builder/), but failing
// early here gives a better message and doesn't waste a build.
func collectModules(dir string) ([]mod, error) {
	goModPath := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(goModPath) //nolint:gosec // dir is user-provided
	if err != nil {
		return nil, err
	}
	mf, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", goModPath, err)
	}

	// Index require versions by module path for the tidiness check.
	requireVersion := make(map[string]string, len(mf.Require))
	for _, r := range mf.Require {
		requireVersion[r.Mod.Path] = r.Mod.Version
	}

	// Classify replaces. Local replaces (New.Version == "") are filesystem
	// paths — `go mod download` skips them and they're handled by the Nix
	// builder via symlinks. Remote replaces affect the lockfile key.
	type replace struct{ origPath string }
	remoteReplace := map[string]replace{} // replacement path -> original
	localReplace := map[string]bool{}     // original path -> true
	for _, r := range mf.Replace {
		if r.New.Version == "" {
			localReplace[r.Old.Path] = true
			continue
		}
		remoteReplace[r.New.Path] = replace{origPath: r.Old.Path}
	}

	cmd := exec.Command("go", "mod", "download", "-json")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go mod download in %s: %w", dir, err)
	}

	var mods []mod
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var dl goModDownload
		if err := dec.Decode(&dl); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding go mod download output: %w", err)
		}
		if dl.Error != "" {
			return nil, fmt.Errorf("go mod download %s@%s: %s", dl.Path, dl.Version, dl.Error)
		}
		if dl.Dir == "" {
			// No local source directory => nothing to hash. In practice this
			// shouldn't happen after a successful download, but guard anyway.
			continue
		}

		// dl.Path is the replacement path if one exists. Remap to the original
		// so the lockfile key matches the `require` directive's path.
		origPath, fetchPath := dl.Path, dl.Path
		if r, ok := remoteReplace[dl.Path]; ok {
			origPath = r.origPath
		}

		// Tidiness check: the MVS-resolved version must match the go.mod
		// require version (unless this is a replaced module, in which case
		// the require version is the pre-replace version and expected to
		// differ). This is the ONLY place we can catch the "other project
		// happens to use the stale version" silent-mismatch bug.
		if want, ok := requireVersion[origPath]; ok && want != dl.Version {
			_, isRemoteReplaced := remoteReplace[dl.Path]
			if !isRemoteReplaced && !localReplace[origPath] {
				return nil, fmt.Errorf(
					"go.mod is not tidy: require %s %s but MVS resolves %s (run `go mod tidy` in %s)",
					origPath, want, dl.Version, dir,
				)
			}
		}

		mods = append(mods, mod{
			key:       origPath + "@" + dl.Version,
			fetchPath: fetchPath,
			version:   dl.Version,
			dir:       dl.Dir,
		})
	}
	return mods, nil
}

// --- Hashing -----------------------------------------------------------------

// narHash computes the NAR hash of a directory via `nix hash path`.
func narHash(dir string) (string, error) {
	out, err := exec.Command("nix", "hash", "path", dir).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("nix hash path %s: %s", dir, ee.Stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// --- Main --------------------------------------------------------------------

func main() {
	log.SetFlags(0)

	output := flag.String("o", "go2nix.toml", "output lockfile path")
	projects := flag.String("projects", "", "file listing project directories, one per line (default: require positional args)")
	jobs := flag.Int("j", runtime.NumCPU(), "max parallel `nix hash path` invocations")
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 && *projects != "" {
		var err error
		dirs, err = readProjects(*projects)
		if err != nil {
			log.Fatalf("reading project list: %v", err)
		}
	}
	if len(dirs) == 0 {
		log.Fatalf("no project directories specified (pass as args or via -projects)")
	}

	if err := generate(dirs, *output, *jobs); err != nil {
		log.Fatal(err)
	}
}

func generate(dirs []string, output string, jobs int) error {
	cache, err := readLockfile(output)
	if err != nil {
		return fmt.Errorf("reading existing lockfile: %w", err)
	}
	log.Printf("cache: %d entries", len(cache.Mod))

	// Collect modules from all projects. The same (module, version) may appear
	// in multiple projects; we only need to hash it once.
	want := map[string]mod{} // key -> mod
	for _, dir := range dirs {
		mods, err := collectModules(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		for _, m := range mods {
			if _, ok := want[m.key]; !ok {
				want[m.key] = m
			}
		}
	}
	log.Printf("want: %d unique modules across %d project(s)", len(want), len(dirs))

	// Determine which modules need hashing. A cache entry is reusable only if
	// the key matches AND the fetchPath matches what was recorded — otherwise
	// a changed replace directive could leave us with the wrong hash.
	var toHash []mod
	result := &lockfile{Mod: map[string]Entry{}}
	for key, m := range want {
		if cached, ok := cache.Mod[key]; ok && cached.Replaced == m.replaced() {
			result.Mod[key] = cached
		} else {
			toHash = append(toHash, m)
		}
	}
	slices.SortFunc(toHash, func(a, b mod) int { return strings.Compare(a.key, b.key) })
	log.Printf("hash: %d modules (%d cached)", len(toHash), len(result.Mod))

	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(jobs)
	for _, m := range toHash {
		g.Go(func() error {
			h, err := narHash(m.dir)
			if err != nil {
				return fmt.Errorf("hashing %s: %w", m.key, err)
			}
			mu.Lock()
			result.Mod[m.key] = Entry{Version: m.version, Hash: h, Replaced: m.replaced()}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	log.Printf("write: %d entries -> %s", len(result.Mod), output)
	return result.write(output, lockfileHeader)
}

// readProjects reads newline-separated directory paths from path.
// Blank lines and '#'-prefixed lines are ignored.
func readProjects(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is user-provided by design
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dirs = append(dirs, line)
	}
	return dirs, nil
}

const lockfileHeader = `# go2nix lockfile: module@version -> NAR hash.
# https://github.com/numtide/go2nix-torture/tree/main/go2nix

`
