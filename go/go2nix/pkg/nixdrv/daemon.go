package nixdrv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nix-community/go-nix/pkg/daemon"
	"github.com/nix-community/go-nix/pkg/nar"
	"github.com/nix-community/go-nix/pkg/storepath"
)

// DaemonStore implements Store via direct nix-daemon socket connections,
// avoiding the per-derivation `nix` CLI subprocess overhead in NixTool.
type DaemonStore struct {
	ctx  context.Context
	pool *daemon.ClientPool
}

var _ Store = (*DaemonStore)(nil)

// ConnectDaemon dials the nix-daemon socket once (verifying reachability),
// seeds a pool with that connection, and returns a DaemonStore that will
// dial up to maxConns total connections on demand.
func ConnectDaemon(ctx context.Context, socketPath string, maxConns int) (*DaemonStore, error) {
	pool, err := daemon.NewClientPool(ctx, socketPath, maxConns)
	if err != nil {
		return nil, err
	}
	return &DaemonStore{ctx: ctx, pool: pool}, nil
}

// Close drains the idle pool and closes each connection. In-flight
// operations complete; their connections are closed on release.
func (s *DaemonStore) Close() error {
	return s.pool.Close()
}

// DerivationAdd registers a derivation by writing its ATerm content to the
// store with text:sha256 content-addressing — the same operation
// `nix derivation add` performs after parsing JSON.
func (s *DaemonStore) DerivationAdd(drv *Derivation) (*storepath.StorePath, error) {
	aterm, refs, err := drv.ATerm()
	if err != nil {
		return nil, err
	}

	var (
		rpcErr error
		info   *daemon.PathInfo
	)

	// acquire a connection from the pool
	c, err := s.pool.Acquire()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire a daemon connection: %w", err)
	}

	// ensure the connection gets released when we're done
	defer func() {
		s.pool.Release(c, rpcErr)
	}()

	// add the derivation
	info, rpcErr = c.AddToStore(s.ctx, &daemon.AddToStoreRequest{
		Name:             drv.name + ".drv",
		CAMethodWithAlgo: "text:sha256",
		References:       refs,
		Source:           bytes.NewReader(aterm),
	})

	if rpcErr != nil {
		return nil, fmt.Errorf("daemon AddToStore for %q: %w", drv.name, rpcErr)
	}

	return storepath.FromAbsolutePath(info.StorePath)
}

// Build realises installables (e.g. "/nix/store/...drv^out") and returns output paths.
// The Store interface accepts CLI-style "^" output separators; the daemon
// worker protocol parses DerivedPath with the legacy "!" separator
// (libstore/worker-protocol.cc → DerivedPath::parseLegacy), so translate.
func (s *DaemonStore) Build(installables ...string) ([]*storepath.StorePath, error) {
	// translate ^ to !
	derivedPaths := make([]string, len(installables))
	for i, inst := range installables {
		derivedPaths[i] = strings.ReplaceAll(inst, "^", "!")
	}

	var (
		rpcErr  error
		results []daemon.BuildResult
	)

	// acquire a connection from the pool
	c, err := s.pool.Acquire()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire a daemon connection: %w", err)
	}

	// ensure the connection gets released when we're done
	defer func() {
		s.pool.Release(c, rpcErr)
	}()

	// build the paths
	if results, rpcErr = c.BuildPathsWithResults(s.ctx, derivedPaths, daemon.BuildModeNormal); rpcErr != nil {
		return nil, fmt.Errorf("daemon BuildPathsWithResults: %w", rpcErr)
	}

	var paths []*storepath.StorePath
	for i, r := range results {
		if r.Status > daemon.BuildStatusAlreadyValid {
			return nil, fmt.Errorf("build %s: %s: %s", installables[i], r.Status, r.ErrorMsg)
		}

		var sp *storepath.StorePath
		for _, realised := range r.BuiltOutputs {
			if sp, err = storepath.FromAbsolutePath(realised.OutPath); err != nil {
				return nil, fmt.Errorf("parsing build output %q: %w", realised.OutPath, err)
			}

			paths = append(paths, sp)
		}
	}

	return paths, nil
}

// StoreAdd recursively imports a local directory by streaming its NAR
// representation to the daemon.
func (s *DaemonStore) StoreAdd(name, path string) (*storepath.StorePath, error) {
	// create a pipe for streaming the NAR to the daemon
	pr, pw := io.Pipe()

	// write the NAR to the pipe asynchronously
	go func() {
		_ = pw.CloseWithError(nar.DumpPath(pw, path))
	}()

	// If AddToStore errors before draining pr, closing it makes the goroutine's
	// next pw.Write return io.ErrClosedPipe and exit instead of blocking forever.
	defer func() { _ = pr.Close() }()

	var (
		rpcErr error
		info   *daemon.PathInfo
	)

	// acquire a connection from the pool
	c, err := s.pool.Acquire()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire a daemon connection: %w", err)
	}

	// ensure the connection gets released when we're done
	defer func() {
		s.pool.Release(c, rpcErr)
	}()

	// add the path
	info, rpcErr = c.AddToStore(s.ctx, &daemon.AddToStoreRequest{
		Name:             name,
		CAMethodWithAlgo: "fixed:r:sha256",

		Source: pr,
	})
	if rpcErr != nil {
		return nil, fmt.Errorf("daemon AddToStore %q: %w", name, rpcErr)
	}

	return storepath.FromAbsolutePath(info.StorePath)
}
