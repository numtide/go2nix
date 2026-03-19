package nixdrv

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/numtide/go2nix/internal/gonix/nixbase32"
	"github.com/numtide/go2nix/internal/gonix/storepath"
)

// Placeholder represents a Nix placeholder hash.
type Placeholder struct {
	hash []byte // 32 bytes (SHA-256)
}

// HashPart returns the nixbase32-encoded hash portion of a store path.
func HashPart(sp *storepath.StorePath) string {
	return nixbase32.EncodeToString(sp.Digest)
}

// DrvName returns the name portion of a .drv store path with the ".drv" suffix stripped.
// Panics if the store path is not a derivation.
func DrvName(sp *storepath.StorePath) string {
	if !strings.HasSuffix(sp.Name, ".drv") {
		panic(fmt.Sprintf("not a derivation path: %s", sp.Absolute()))
	}
	return strings.TrimSuffix(sp.Name, ".drv")
}

// StandardOutput creates a placeholder for a simple derivation output.
// Format: SHA256("nix-output:<output_name>")
func StandardOutput(outputName string) Placeholder {
	h := sha256.Sum256([]byte("nix-output:" + outputName))
	return Placeholder{hash: h[:]}
}

// CAOutput creates a placeholder for a content-addressed derivation output.
// The drvPath must be a .drv store path.
// Format: SHA256("nix-upstream-output:<drv_hash_part>:<output_path_name>")
func CAOutput(drvPath *storepath.StorePath, outputName string) Placeholder {
	drvName := DrvName(drvPath)
	outputPathName := OutputPathName(drvName, outputName)
	clearText := "nix-upstream-output:" + HashPart(drvPath) + ":" + outputPathName
	h := sha256.Sum256([]byte(clearText))
	return Placeholder{hash: h[:]}
}

// DynamicOutput creates a placeholder for a dynamically-created derivation output.
// Format: SHA256("nix-computed-output:<nix-base32(compress(placeholder.hash, 20))>:<output_name>")
func DynamicOutput(p Placeholder, outputName string) Placeholder {
	compressed := compressHash(p.hash, 20)
	compressedStr := nixbase32.EncodeToString(compressed)
	clearText := "nix-computed-output:" + compressedStr + ":" + outputName
	h := sha256.Sum256([]byte(clearText))
	return Placeholder{hash: h[:]}
}

// Render returns the placeholder string as it appears in derivation env vars.
// Format: /<nix-base32-encoded-hash>
func (p Placeholder) Render() string {
	return "/" + nixbase32.EncodeToString(p.hash)
}

// OutputPathName returns the output path name for a derivation.
// If outputName is "out", returns drvName; otherwise drvName-outputName.
func OutputPathName(drvName, outputName string) string {
	if outputName == "out" {
		return drvName
	}
	return drvName + "-" + outputName
}

// compressHash XOR-compresses a hash to a shorter length.
func compressHash(hash []byte, newSize int) []byte {
	result := make([]byte, newSize)
	for i, b := range hash {
		result[i%newSize] ^= b
	}
	return result
}
