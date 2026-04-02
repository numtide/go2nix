package nixdrv

import (
	"context"
	"os"
	"testing"
)

// daemonSocket returns the nix-daemon socket path from the environment, or "" if unavailable.
func daemonSocket() string {
	if p := os.Getenv("NIX_DAEMON_SOCKET_PATH"); p != "" {
		return p
	}
	const def = "/nix/var/nix/daemon-socket/socket"
	if _, err := os.Stat(def); err == nil {
		return def
	}
	return ""
}

// TestDaemonDerivationAddMatchesDrvPath verifies the daemon's text:sha256
// AddToStore returns the same .drv path that DrvPath() computes in-process.
// Both implement the algorithm `nix derivation add` uses.
func TestDaemonDerivationAddMatchesDrvPath(t *testing.T) {
	sock := daemonSocket()
	if sock == "" {
		t.Skip("no nix-daemon socket available")
	}

	ds, err := ConnectDaemon(context.Background(), sock)
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

// TestDaemonStoreAddRoundTrip verifies StoreAdd produces a valid store path.
func TestDaemonStoreAddRoundTrip(t *testing.T) {
	sock := daemonSocket()
	if sock == "" {
		t.Skip("no nix-daemon socket available")
	}

	ds, err := ConnectDaemon(context.Background(), sock)
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
