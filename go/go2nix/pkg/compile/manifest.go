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
	defer f.Close()

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
				w.WriteString(line)
				w.WriteByte('\n')
			}
		}
		if err := scanner.Err(); err != nil {
			pf.Close()
			return "", fmt.Errorf("reading importcfg part %s: %w", part, err)
		}
		pf.Close()
	}

	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("writing merged importcfg: %w", err)
	}
	return outPath, nil
}
