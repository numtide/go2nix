package derivation

import (
	"github.com/numtide/go2nix/internal/gonix/storepath"
)

type Output struct {
	Path          string `json:"path"`
	HashAlgorithm string `json:"hashAlgo,omitempty"`
	Hash          string `json:"hash,omitempty"`
}

func (o *Output) Validate() error {
	return storepath.Validate(o.Path)
}
