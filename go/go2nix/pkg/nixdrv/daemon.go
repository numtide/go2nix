package nixdrv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/nix-community/go-nix/pkg/daemon"
	"github.com/nix-community/go-nix/pkg/nar"
	"github.com/nix-community/go-nix/pkg/storepath"
)

// DaemonStore implements Store via a direct nix-daemon socket connection,
// avoiding the per-derivation `nix` CLI subprocess overhead in NixTool.
//
// daemon.Client is not safe for concurrent use, so calls are serialized
// behind a mutex. Callers that previously fanned out NixTool.DerivationAdd
// across N goroutines see no parallelism here, but the per-call cost drops
// from a fork+exec+CLI-parse round-trip to a socket write+read.
type DaemonStore struct {
	mu     sync.Mutex
	client *daemon.Client
	ctx    context.Context
}

var _ Store = (*DaemonStore)(nil)

// ConnectDaemon dials the nix-daemon socket and returns a DaemonStore.
func ConnectDaemon(ctx context.Context, socketPath string) (*DaemonStore, error) {
	c, err := daemon.Connect(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	return &DaemonStore{client: c, ctx: ctx}, nil
}

// Close releases the daemon connection.
func (s *DaemonStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client.Close()
}

// DerivationAdd registers a derivation by writing its ATerm content to the
// store with text:sha256 content-addressing — the same operation
// `nix derivation add` performs after parsing JSON.
func (s *DaemonStore) DerivationAdd(drv *Derivation) (*storepath.StorePath, error) {
	aterm, refs, err := drv.ATerm()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := s.client.AddToStore(s.ctx, &daemon.AddToStoreRequest{
		Name:             drv.name + ".drv",
		CAMethodWithAlgo: "text:sha256",
		References:       refs,
		Source:           bytes.NewReader(aterm),
	})
	if err != nil {
		return nil, fmt.Errorf("daemon AddToStore for %q: %w", drv.name, err)
	}
	return storepath.FromAbsolutePath(info.StorePath)
}

// Build realises installables (e.g. "/nix/store/...drv^out") and returns output paths.
func (s *DaemonStore) Build(installables ...string) ([]*storepath.StorePath, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	results, err := s.client.BuildPathsWithResults(s.ctx, installables, daemon.BuildModeNormal)
	if err != nil {
		return nil, fmt.Errorf("daemon BuildPathsWithResults: %w", err)
	}

	var paths []*storepath.StorePath
	for i, r := range results {
		if r.Status > daemon.BuildStatusAlreadyValid {
			return nil, fmt.Errorf("build %s: %s: %s", installables[i], r.Status, r.ErrorMsg)
		}
		for _, real := range r.BuiltOutputs {
			sp, err := storepath.FromAbsolutePath(real.OutPath)
			if err != nil {
				return nil, fmt.Errorf("parsing build output %q: %w", real.OutPath, err)
			}
			paths = append(paths, sp)
		}
	}
	return paths, nil
}

// StoreAdd recursively imports a local directory by streaming its NAR
// representation to the daemon.
func (s *DaemonStore) StoreAdd(name, path string) (*storepath.StorePath, error) {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(nar.DumpPath(pw, path))
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := s.client.AddToStore(s.ctx, &daemon.AddToStoreRequest{
		Name:             name,
		CAMethodWithAlgo: "fixed:r:sha256",
		Source:           pr,
	})
	if err != nil {
		return nil, fmt.Errorf("daemon AddToStore %q: %w", name, err)
	}
	return storepath.FromAbsolutePath(info.StorePath)
}
