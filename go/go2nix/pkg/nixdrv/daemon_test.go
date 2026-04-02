package nixdrv

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
)

// nixSystem returns the Nix system string for the current host.
func nixSystem() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	return arch + "-" + runtime.GOOS
}

// daemonSocket returns the nix-daemon socket path from the environment, or "" if unavailable.
func daemonSocket(t *testing.T) string {
	if p := os.Getenv("NIX_DAEMON_SOCKET_PATH"); p != "" {
		return p
	}

	const def = "/nix/var/nix/daemon-socket/socket"
	if _, err := os.Stat(def); err == nil {
		return def
	}

	t.Skip("no nix-daemon socket available")

	return ""
}

// TestDaemonDerivationAddMatchesDrvPath verifies the daemon's text:sha256
// AddToStore returns the same .drv path that DrvPath() computes in-process.
// Both implement the algorithm `nix derivation add` uses.
func TestDaemonDerivationAddMatchesDrvPath(t *testing.T) {
	sock := daemonSocket(t)

	ds, err := ConnectDaemon(t.Context(), sock, 4)
	if err != nil {
		t.Skipf("daemon connect failed: %v", err)
	}
	defer func() { _ = ds.Close() }()

	drv := NewDerivation("go2nix-daemon-test", "x86_64-linux", "/bin/sh").
		AddCAOutput("out", "sha256", "nar").
		SetEnv("foo", "bar")

	want, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("DrvPath: %v", err)
	}

	got, err := ds.DerivationAdd(drv)
	if err != nil {
		t.Fatalf("DerivationAdd: %v", err)
	}

	if got.String() != want.String() {
		t.Errorf("daemon path = %s, in-process path = %s", got, want)
	}
}

// TestDaemonBuildWarmReturnsOutputs verifies that DaemonStore.Build returns
// output paths even when the derivation is already valid (warm cache).
// buildFODs in pkg/resolve relies on len(outputs) == len(installables); if
// BuildPathsWithResults returned empty BuiltOutputs for AlreadyValid, that
// invariant would break on the second build of any module set.
func TestDaemonBuildWarmReturnsOutputs(t *testing.T) {
	sock := daemonSocket(t)
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this system")
	}

	ds, err := ConnectDaemon(t.Context(), sock, 4)
	if err != nil {
		t.Skipf("daemon connect failed: %v", err)
	}
	defer func() { _ = ds.Close() }()

	// CA-floating derivation; /bin/sh is in nix's default sandbox-paths.
	drv := NewDerivation("go2nix-daemon-warm-test", nixSystem(), "/bin/sh").
		AddArg("-c").
		AddArg(`echo warm-test > "$out"`).
		AddCAOutput("out", "sha256", "nar")

	drvPath, err := ds.DerivationAdd(drv)
	if err != nil {
		t.Fatalf("DerivationAdd: %v", err)
	}
	inst := drvPath.Absolute() + "^out"

	cold, err := ds.Build(inst)
	if err != nil {
		t.Skipf("cold build failed (sandbox/builder config?): %v", err)
	}
	if len(cold) != 1 {
		t.Fatalf("cold build returned %d paths, want 1", len(cold))
	}

	warm, err := ds.Build(inst)
	if err != nil {
		t.Fatalf("warm build: %v", err)
	}
	if len(warm) != 1 {
		t.Fatalf("warm build returned %d paths, want 1 — BuiltOutputs not populated for AlreadyValid?", len(warm))
	}
	if warm[0].String() != cold[0].String() {
		t.Errorf("warm path %s != cold path %s", warm[0], cold[0])
	}
}

// TestDaemonStoreAddRoundTrip verifies StoreAdd produces a valid store path.
func TestDaemonStoreAddRoundTrip(t *testing.T) {
	sock := daemonSocket(t)

	ds, err := ConnectDaemon(t.Context(), sock, 4)
	if err != nil {
		t.Skipf("daemon connect failed: %v", err)
	}
	defer func() { _ = ds.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sp, err := ds.StoreAdd("go2nix-daemon-storeadd-test", dir)
	if err != nil {
		t.Fatalf("StoreAdd: %v", err)
	}
	if _, err := os.Stat(sp.Absolute()); err != nil {
		t.Errorf("store path not present on disk: %v", err)
	}
}

// TestDaemonPoolConcurrentDerivationAdd exercises the connection pool with
// more goroutines than maxConns. Each derivation has a unique name so the
// daemon does real work per call. With -race this catches pool/semaphore
// misuse and verifies a connection that errored isn't reused.
func TestDaemonPoolConcurrentDerivationAdd(t *testing.T) {
	sock := daemonSocket(t)
	if sock == "" {
		t.Skip("no nix-daemon socket available")
	}

	const maxConns, workers, perWorker = 4, 16, 8

	ds, err := ConnectDaemon(t.Context(), sock, maxConns)
	if err != nil {
		t.Skipf("daemon connect failed: %v", err)
	}
	defer func() { _ = ds.Close() }()

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWorker {
				drv := NewDerivation(fmt.Sprintf("go2nix-pool-test-%d-%d", w, i), nixSystem(), "/bin/sh").
					AddCAOutput("out", "sha256", "nar")
				if _, err := ds.DerivationAdd(drv); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
