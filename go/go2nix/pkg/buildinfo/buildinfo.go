// Package buildinfo generates the modinfo directive for go tool link's
// importcfg, matching cmd/go's writeLinkImportcfg behavior.
//
// The linker reads "modinfo <quoted>" from importcfg and embeds it
// as runtime.modinfo, which is what go version -m reads.
package buildinfo

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// Magic markers wrapping the modinfo string (from cmd/go/internal/modload/build.go).
var (
	infoStart, _ = hex.DecodeString("3077af0c9274080241e1c107e6d618e6")
	infoEnd, _   = hex.DecodeString("f932433186182072008242104116d8f2")
)

// ModDep describes a module dependency for modinfo generation.
type ModDep struct {
	Path    string
	Version string
	Replace *ModDep // non-nil if this module is replaced
}

// GenerateModinfo produces the modinfo importcfg line for the linker.
//
// moduleRoot is the path to the directory containing go.mod (and optionally
// go.sum for checksums). goVersion is the full Go toolchain version string
// (e.g., "go1.21.5"). deps lists all third-party module dependencies.
//
// Returns a string like: modinfo "..."
func GenerateModinfo(moduleRoot, goVersion string, deps []ModDep) (string, error) {
	// Parse go.mod for the main module path.
	goModPath := filepath.Join(moduleRoot, "go.mod")
	goModData, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	mf, err := modfile.ParseLax(goModPath, goModData, nil)
	if err != nil {
		return "", fmt.Errorf("parsing go.mod: %w", err)
	}
	if mf.Module == nil {
		return "", fmt.Errorf("go.mod missing module directive")
	}

	// Read go.sum for checksums (optional).
	sums := readGoSum(filepath.Join(moduleRoot, "go.sum"))

	// Build debug.BuildInfo.
	info := &debug.BuildInfo{
		GoVersion: goVersion,
		Path:      mf.Module.Mod.Path,
		Main: debug.Module{
			Path:    mf.Module.Mod.Path,
			Version: "(devel)",
		},
	}

	// Sort deps for deterministic output.
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Path < deps[j].Path
	})

	for _, dep := range deps {
		dm := debug.Module{
			Path:    dep.Path,
			Version: dep.Version,
		}
		// Look up go.sum hash for this module.
		sumKey := dep.Path + "@" + dep.Version
		if h, ok := sums[sumKey]; ok {
			dm.Sum = h
		}
		if dep.Replace != nil {
			dm.Replace = &debug.Module{
				Path:    dep.Replace.Path,
				Version: dep.Replace.Version,
			}
			replKey := dep.Replace.Path + "@" + dep.Replace.Version
			if h, ok := sums[replKey]; ok {
				dm.Replace.Sum = h
			}
		}
		info.Deps = append(info.Deps, &dm)
	}

	// Wrap with magic markers, matching ModInfoData().
	modInfoData := string(infoStart) + info.String() + string(infoEnd)
	return fmt.Sprintf("modinfo %s", strconv.Quote(modInfoData)), nil
}

// readGoSum parses a go.sum file and returns a map of "path@version" → hash.
// Only directory hashes (not /go.mod hashes) are kept.
// Returns an empty map if the file can't be read.
func readGoSum(path string) map[string]string {
	result := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		// Format: <module> <version> <hash>
		// Skip /go.mod entries — we only want directory hashes.
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		if strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		key := fields[0] + "@" + fields[1]
		result[key] = fields[2]
	}
	return result
}
