package compile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ManifestVersion is the current schema version for all manifest types.
	ManifestVersion = 1

	// ManifestKindCompile is the kind value for compile manifests.
	ManifestKindCompile = "compile"

	// ImportcfgPlaceholder is substituted at build time with the actual
	// importcfg path. Used in dynamic mode where the path depends on
	// $NIX_BUILD_TOP which is only known inside the build sandbox.
	ImportcfgPlaceholder = "@@IMPORTCFG@@"
)

// CompileManifest is the JSON contract between Nix and go2nix compile-package.
// Written by Nix at eval time via builtins.toFile, read by Go at build time.
type CompileManifest struct {
	Version        int      `json:"version"`
	Kind           string   `json:"kind"`
	ImportcfgParts []string `json:"importcfgParts"`
	Tags           []string `json:"tags"`
	GCFlags        []string `json:"gcflags"`
	PGOProfile     *string  `json:"pgoProfile"`
}

// LoadCompileManifest reads and validates a compile manifest from path.
func LoadCompileManifest(path string) (*CompileManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading compile manifest: %w", err)
	}
	var m CompileManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing compile manifest: %w", err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("compile manifest: unsupported version %d (expected %d)", m.Version, ManifestVersion)
	}
	if m.Kind != ManifestKindCompile {
		return nil, fmt.Errorf("compile manifest: wrong kind %q (expected %q)", m.Kind, ManifestKindCompile)
	}
	return &m, nil
}

// ManifestKindLink is the kind value for link manifests.
const ManifestKindLink = "link"

// LinkManifest is the JSON contract between Nix and go2nix link-binary.
type LinkManifest struct {
	Version        int               `json:"version"`
	Kind           string            `json:"kind"`
	ImportcfgParts []string          `json:"importcfgParts"`
	LocalArchives  map[string]string `json:"localArchives"`
	// Optional interface-split mode: when set, the main-package compile
	// reads these (export-data .x files) instead of ImportcfgParts/
	// LocalArchives, so its inputs are stable across changes that don't
	// touch any dependency's exported API. The link step still uses
	// ImportcfgParts/LocalArchives (.a link objects).
	CompileImportcfgParts []string          `json:"compileImportcfgParts,omitempty"`
	LocalIfaces           map[string]string `json:"localIfaces,omitempty"`
	SubPackages           []string          `json:"subPackages"`
	ModuleRoot            string            `json:"moduleRoot"`
	Lockfile              *string           `json:"lockfile"`
	Pname                 string            `json:"pname"`
	GOOS                  *string           `json:"goos"`
	GOARCH                *string           `json:"goarch"`
	LDFlags               []string          `json:"ldflags"`
	GCFlags               []string          `json:"gcflags"`
	Tags                  []string          `json:"tags"`
	PGOProfile            *string           `json:"pgoProfile"`
}

// LoadLinkManifest reads and validates a link manifest from path.
func LoadLinkManifest(path string) (*LinkManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading link manifest: %w", err)
	}
	var m LinkManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing link manifest: %w", err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("link manifest: unsupported version %d (expected %d)", m.Version, ManifestVersion)
	}
	if m.Kind != ManifestKindLink {
		return nil, fmt.Errorf("link manifest: wrong kind %q (expected %q)", m.Kind, ManifestKindLink)
	}
	return &m, nil
}

// ManifestKindTest is the kind value for test manifests.
const ManifestKindTest = "test"

// TestManifest is the JSON contract between Nix and go2nix test-packages.
type TestManifest struct {
	Version        int               `json:"version"`
	Kind           string            `json:"kind"`
	ImportcfgParts []string          `json:"importcfgParts"`
	LocalArchives  map[string]string `json:"localArchives"`
	// Optional interface-split mode: when set, test-package compiles
	// (internal/xtest/_testmain) read these (export-data .x files)
	// instead of ImportcfgParts/LocalArchives, so they're stable across
	// changes that don't touch any local dep's exported API. The link
	// step still uses ImportcfgParts/LocalArchives (.a link objects).
	CompileImportcfgParts []string          `json:"compileImportcfgParts,omitempty"`
	LocalIfaces           map[string]string `json:"localIfaces,omitempty"`
	ModuleRoot            string            `json:"moduleRoot"`
	Tags                  []string          `json:"tags"`
	GCFlags               []string          `json:"gcflags"`
	CheckFlags            []string          `json:"checkFlags"`
}

// LoadTestManifest reads and validates a test manifest from path.
func LoadTestManifest(path string) (*TestManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading test manifest: %w", err)
	}
	var m TestManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing test manifest: %w", err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("test manifest: unsupported version %d (expected %d)", m.Version, ManifestVersion)
	}
	if m.Kind != ManifestKindTest {
		return nil, fmt.Errorf("test manifest: wrong kind %q (expected %q)", m.Kind, ManifestKindTest)
	}
	return &m, nil
}

// MergeImportcfg merges multiple importcfg files into a single file.
// Each input file is read line by line; lines starting with "packagefile "
// or "importmap " are included. Blank lines and comments are skipped.
// The merged result is written to a temporary file under tmpDir and the
// path is returned.
func MergeImportcfg(parts []string, tmpDir string) (string, error) {
	outPath := filepath.Join(tmpDir, "importcfg.merged")
	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("creating merged importcfg: %w", err)
	}
	defer f.Close() //nolint:errcheck

	w := bufio.NewWriter(f)
	for _, part := range parts {
		pf, err := os.Open(part)
		if err != nil {
			return "", fmt.Errorf("opening importcfg part %s: %w", part, err)
		}
		scanner := bufio.NewScanner(pf)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "packagefile ") || strings.HasPrefix(line, "importmap ") {
				_, _ = w.WriteString(line)
				_ = w.WriteByte('\n')
			}
		}
		if err := scanner.Err(); err != nil {
			_ = pf.Close()
			return "", fmt.Errorf("reading importcfg part %s: %w", part, err)
		}
		if err := pf.Close(); err != nil {
			return "", fmt.Errorf("closing importcfg part %s: %w", part, err)
		}
	}

	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("writing merged importcfg: %w", err)
	}
	return outPath, nil
}
