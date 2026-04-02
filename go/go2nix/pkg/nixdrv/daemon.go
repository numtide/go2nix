package nixdrv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/nix-community/go-nix/pkg/daemon"
	"github.com/nix-community/go-nix/pkg/nar"
	"github.com/nix-community/go-nix/pkg/storepath"
)

// DaemonStore implements Store via direct nix-daemon socket connections,
// avoiding the per-derivation `nix` CLI subprocess overhead in NixTool.
//
// daemon.Client is not safe for concurrent use, so DaemonStore maintains a
// bounded pool of connections. Each operation checks one out, uses it
// exclusively, and returns it. This restores the parallelism that
// registerDerivations gets via errgroup.SetLimit(NixJobs) — instead of
// serializing N goroutines behind a single mutex, up to maxConns operations
// proceed in parallel on independent sockets.
type DaemonStore struct {
	ctx        context.Context
	socketPath string

	idle chan *daemon.Client // returned connections; cap = maxConns
	sem  chan struct{}       // counting semaphore for total live connections

	closeOnce sync.Once
	closed    chan struct{}
}

var _ Store = (*DaemonStore)(nil)

// ConnectDaemon dials the nix-daemon socket once (verifying reachability),
// seeds a pool with that connection, and returns a DaemonStore that will
// dial up to maxConns total connections on demand.
func ConnectDaemon(ctx context.Context, socketPath string, maxConns int) (*DaemonStore, error) {
	if maxConns < 1 {
		maxConns = 1
	}
	c, err := daemon.Connect(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	s := &DaemonStore{
		ctx:        ctx,
		socketPath: socketPath,
		idle:       make(chan *daemon.Client, maxConns),
		sem:        make(chan struct{}, maxConns),
		closed:     make(chan struct{}),
	}
	s.sem <- struct{}{}
	s.idle <- c
	return s, nil
}

// acquire returns a connection from the pool, dialling a new one if the
// pool is empty and the live-connection budget allows. Blocks if maxConns
// connections are already checked out.
func (s *DaemonStore) acquire() (*daemon.Client, error) {
	for {
		select {
		case <-s.closed:
			return nil, fmt.Errorf("DaemonStore is closed")
		case c := <-s.idle:
			return c, nil
		case s.sem <- struct{}{}:
			c, err := daemon.Connect(s.ctx, s.socketPath)
			if err != nil {
				<-s.sem
				return nil, err
			}
			return c, nil
		}
	}
}

// release returns a connection to the pool. If the operation that used it
// errored, the connection is closed instead — a protocol error can leave the
// stream in an undefined state, and the next acquire will dial fresh.
func (s *DaemonStore) release(c *daemon.Client, opErr error) {
	if opErr != nil {
		_ = c.Close()
		<-s.sem
		return
	}
	select {
	case s.idle <- c:
	case <-s.closed:
		_ = c.Close()
		<-s.sem
	}
}

// Close drains the idle pool and closes each connection. In-flight
// operations complete; their connections are closed on release.
func (s *DaemonStore) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		for {
			select {
			case c := <-s.idle:
				_ = c.Close()
				<-s.sem
			default:
				return
			}
		}
	})
	return nil
}

// DerivationAdd registers a derivation by writing its ATerm content to the
// store with text:sha256 content-addressing — the same operation
// `nix derivation add` performs after parsing JSON.
func (s *DaemonStore) DerivationAdd(drv *Derivation) (sp *storepath.StorePath, err error) {
	aterm, refs, err := drv.ATerm()
	if err != nil {
		return nil, err
	}

	c, err := s.acquire()
	if err != nil {
		return nil, err
	}
	defer func() { s.release(c, err) }()

	info, err := c.AddToStore(s.ctx, &daemon.AddToStoreRequest{
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
// The Store interface accepts CLI-style "^" output separators; the daemon
// worker protocol parses DerivedPath with the legacy "!" separator
// (libstore/worker-protocol.cc → DerivedPath::parseLegacy), so translate.
func (s *DaemonStore) Build(installables ...string) (paths []*storepath.StorePath, err error) {
	derivedPaths := make([]string, len(installables))
	for i, inst := range installables {
		derivedPaths[i] = strings.ReplaceAll(inst, "^", "!")
	}

	c, err := s.acquire()
	if err != nil {
		return nil, err
	}
	defer func() { s.release(c, err) }()

	results, err := c.BuildPathsWithResults(s.ctx, derivedPaths, daemon.BuildModeNormal)
	if err != nil {
		return nil, fmt.Errorf("daemon BuildPathsWithResults: %w", err)
	}

	for i, r := range results {
		if r.Status > daemon.BuildStatusAlreadyValid {
			return nil, fmt.Errorf("build %s: %s: %s", installables[i], r.Status, r.ErrorMsg)
		}

		for _, real := range r.BuiltOutputs {
			sp, perr := storepath.FromAbsolutePath(real.OutPath)
			if perr != nil {
				return nil, fmt.Errorf("parsing build output %q: %w", real.OutPath, perr)
			}
			paths = append(paths, sp)
		}
	}

	return paths, nil
}

// StoreAdd recursively imports a local directory by streaming its NAR
// representation to the daemon.
func (s *DaemonStore) StoreAdd(name, path string) (sp *storepath.StorePath, err error) {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(nar.DumpPath(pw, path))
	}()

	// If AddToStore errors before draining pr, closing it makes the goroutine's
	// next pw.Write return io.ErrClosedPipe and exit instead of blocking forever.
	defer func() { _ = pr.Close() }()

	c, err := s.acquire()
	if err != nil {
		return nil, err
	}
	defer func() { s.release(c, err) }()

	info, err := c.AddToStore(s.ctx, &daemon.AddToStoreRequest{
		Name:             name,
		CAMethodWithAlgo: "fixed:r:sha256",

		Source: pr,
	})
	if err != nil {
		return nil, fmt.Errorf("daemon AddToStore %q: %w", name, err)
	}

	return storepath.FromAbsolutePath(info.StorePath)
}
