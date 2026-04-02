package nixdrv

import "github.com/nix-community/go-nix/pkg/storepath"

// Store is the subset of nix-store operations the resolve flow needs.
// Implemented by NixTool (CLI subprocess) and DaemonStore (direct socket).
type Store interface {
	// DerivationAdd registers a derivation with the store and returns its .drv path.
	DerivationAdd(drv *Derivation) (*storepath.StorePath, error)
	// Build realises one or more installables (e.g. "/nix/store/...drv^out") and returns output paths.
	Build(installables ...string) ([]*storepath.StorePath, error)
	// StoreAdd recursively adds a local directory to the store under the given name.
	StoreAdd(name, path string) (*storepath.StorePath, error)
}

var _ Store = (*NixTool)(nil)
