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

// debugModFromDep converts a ModDep to a *debug.Module, mirroring cmd/go's
// debugModFromModinfo (load/pkg.go:2334-2349): empty version becomes
// "(devel)", and Sum is read from go.sum only when there is *no* replace
// (so a replaced dep records the replacement's sum, not the original's).
func debugModFromDep(d ModDep, sums map[string]string) *debug.Module {
	version := d.Version
	if version == "" {
		version = "(devel)"
	}
	dm := &debug.Module{Path: d.Path, Version: version}
	if d.Replace != nil {
		dm.Replace = debugModFromDep(*d.Replace, sums)
	} else if d.Version != "" {
		dm.Sum = sums[d.Path+"@"+d.Version]
	}
	return dm
}

// BuildSettings holds the build configuration to embed as `build` lines in
// the binary's modinfo, matching the subset of cmd/go's setBuildInfo that
// go2nix can determine. Empty string fields are omitted from the output
// (except -compiler and -trimpath, which are always emitted).
//
// -ldflags / -gcflags / -asmflags / CGO_*FLAGS are deliberately absent:
// go2nix always builds with trimpath semantics, and cmd/go omits these
// settings under -trimpath because their values can leak system paths
// (see go.dev/issue/52372 and cmd/go/internal/load/pkg.go setBuildInfo).
type BuildSettings struct {
	BuildMode      string // -buildmode (e.g., "exe", "pie")
	Tags           string // -tags as a comma-separated list
	DefaultGODEBUG string // from go.mod's go directive
	CGOEnabled     string // "0" or "1"
	GOARCH         string
	GOARCHLevel    string // value of GO<ARCH> (e.g., "v1" for GOAMD64); key derived from GOARCH
	GOFIPS140      string // emitted only when Fips140Enabled
	GOOS           string
}

// Fips140Enabled reports whether v (a GOFIPS140 value) enables FIPS mode,
// mirroring cmd/go/internal/fips140.Enabled: anything besides "" or "off".
func Fips140Enabled(v string) bool {
	return v != "" && v != "off"
}

// archLevelVar maps GOARCH to the GO<ARCH> environment variable name,
// matching cmd/go's cfg.GOGOARCH().
var archLevelVar = map[string]string{
	"386": "GO386", "amd64": "GOAMD64", "arm": "GOARM", "arm64": "GOARM64",
	"mips": "GOMIPS", "mipsle": "GOMIPS", "mips64": "GOMIPS64", "mips64le": "GOMIPS64",
	"ppc64": "GOPPC64", "ppc64le": "GOPPC64", "riscv64": "GORISCV64", "wasm": "GOWASM",
}

// ArchLevelVar returns the GO<ARCH> environment variable name for goarch
// (e.g., "GOAMD64" for "amd64"), or "" if there is none.
func ArchLevelVar(goarch string) string {
	return archLevelVar[goarch]
}

// toDebugSettings converts BuildSettings to the ordered slice cmd/go emits
// (see src/cmd/go/internal/load/pkg.go:setBuildInfo).
func (s BuildSettings) toDebugSettings() []debug.BuildSetting {
	var out []debug.BuildSetting
	add := func(k, v string) {
		out = append(out, debug.BuildSetting{Key: k, Value: v})
	}
	if s.BuildMode != "" {
		add("-buildmode", s.BuildMode)
	}
	add("-compiler", "gc")
	if s.Tags != "" {
		add("-tags", s.Tags)
	}
	// go2nix always builds with trimpath semantics.
	add("-trimpath", "true")
	if s.DefaultGODEBUG != "" {
		add("DefaultGODEBUG", s.DefaultGODEBUG)
	}
	if s.CGOEnabled != "" {
		add("CGO_ENABLED", s.CGOEnabled)
	}
	if s.GOARCH != "" {
		add("GOARCH", s.GOARCH)
	}
	if Fips140Enabled(s.GOFIPS140) {
		add("GOFIPS140", s.GOFIPS140)
	}
	if s.GOOS != "" {
		add("GOOS", s.GOOS)
	}
	if key := archLevelVar[s.GOARCH]; key != "" && s.GOARCHLevel != "" {
		add(key, s.GOARCHLevel)
	}
	return out
}

// GenerateModinfo produces the modinfo importcfg line for the linker.
//
// moduleRoot is the path to the directory containing go.mod (and optionally
// go.sum for checksums). mainPath is the import path of the main package
// being linked (recorded as debug.BuildInfo.Path so `go version -m` shows
// the per-binary path, matching `go build`); when empty it falls back to
// the module path. goVersion is the full Go toolchain version string
// (e.g., "go1.21.5"). deps lists all third-party module dependencies.
// settings supplies the `build` section so debug.ReadBuildInfo() consumers
// (govulncheck, prometheus go_build_info, SBOM tools) see the same metadata
// as a `go build -trimpath` binary.
//
// Returns a string like: modinfo "..."
func GenerateModinfo(moduleRoot, mainPath, goVersion string, deps []ModDep, settings BuildSettings) (string, error) {
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
	if mainPath == "" {
		mainPath = mf.Module.Mod.Path
	}

	// Read go.sum for checksums (optional).
	sums := readGoSum(filepath.Join(moduleRoot, "go.sum"))

	// Build debug.BuildInfo.
	info := &debug.BuildInfo{
		GoVersion: goVersion,
		Path:      mainPath,
		Main: debug.Module{
			Path:    mf.Module.Mod.Path,
			Version: "(devel)",
		},
		Settings: settings.toDebugSettings(),
	}

	// Sort deps for deterministic output.
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Path < deps[j].Path
	})

	for _, dep := range deps {
		info.Deps = append(info.Deps, debugModFromDep(dep, sums))
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
	defer f.Close() //nolint:errcheck

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
