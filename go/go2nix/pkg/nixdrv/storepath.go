package nixdrv

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nix-community/go-nix/pkg/storepath"
)

const hashLen = 32

// StorePath represents a validated Nix store path.
// Format: /nix/store/<32-char-hash>-<name>
type StorePath struct {
	path string
}

// ParseStorePath validates and returns a StorePath.
func ParseStorePath(path string) (StorePath, error) {
	name := filepath.Base(path)
	if len(name) < hashLen+2 { // hash + dash + at least 1 char
		return StorePath{}, fmt.Errorf("invalid store path %q: too short", path)
	}
	if name[hashLen] != '-' {
		return StorePath{}, fmt.Errorf("invalid store path %q: expected dash at position %d", path, hashLen)
	}
	if !strings.HasPrefix(path, storepath.StoreDir+"/") {
		return StorePath{}, fmt.Errorf("invalid store path %q: must start with %s/", path, storepath.StoreDir)
	}
	return StorePath{path: path}, nil
}

// MustParseStorePath is like ParseStorePath but panics on error.
func MustParseStorePath(path string) StorePath {
	sp, err := ParseStorePath(path)
	if err != nil {
		panic(err)
	}
	return sp
}

// String returns the full store path.
func (sp StorePath) String() string {
	return sp.path
}

// HashPart returns the 32-character hash portion.
func (sp StorePath) HashPart() string {
	return filepath.Base(sp.path)[:hashLen]
}

// Name returns the name portion (after the hash and dash).
func (sp StorePath) Name() string {
	return filepath.Base(sp.path)[hashLen+1:]
}

// IsDerivation returns true if the path ends with ".drv".
func (sp StorePath) IsDerivation() bool {
	return strings.HasSuffix(sp.path, ".drv")
}

// DrvName returns the name with ".drv" suffix stripped.
// Panics if not a derivation path.
func (sp StorePath) DrvName() string {
	n := sp.Name()
	if !strings.HasSuffix(n, ".drv") {
		panic(fmt.Sprintf("not a derivation path: %s", sp.path))
	}
	return strings.TrimSuffix(n, ".drv")
}
