package compile

import (
	"os"
	"testing"
)

func TestMaxWorkers(t *testing.T) {
	// Without NIX_BUILD_CORES, uses NumCPU (capped by n).
	os.Unsetenv("NIX_BUILD_CORES")
	got := maxWorkers(1000)
	if got < 1 {
		t.Errorf("expected >= 1, got %d", got)
	}

	// Capped by n.
	got = maxWorkers(1)
	if got != 1 {
		t.Errorf("cap by n: got %d, want 1", got)
	}

	// Respects NIX_BUILD_CORES.
	t.Setenv("NIX_BUILD_CORES", "4")
	got = maxWorkers(1000)
	if got != 4 {
		t.Errorf("NIX_BUILD_CORES=4: got %d, want 4", got)
	}

	// NIX_BUILD_CORES capped by n.
	got = maxWorkers(2)
	if got != 2 {
		t.Errorf("NIX_BUILD_CORES=4, n=2: got %d, want 2", got)
	}

	// Invalid NIX_BUILD_CORES ignored.
	t.Setenv("NIX_BUILD_CORES", "garbage")
	got = maxWorkers(1000)
	if got < 1 {
		t.Errorf("invalid NIX_BUILD_CORES: got %d", got)
	}

	// NIX_BUILD_CORES=0 means "all cores" in Nix convention; falls back to NumCPU.
	t.Setenv("NIX_BUILD_CORES", "0")
	got = maxWorkers(1000)
	if got < 1 {
		t.Errorf("NIX_BUILD_CORES=0: got %d, want >= 1", got)
	}

	// Negative NIX_BUILD_CORES ignored, falls back to NumCPU.
	t.Setenv("NIX_BUILD_CORES", "-1")
	got = maxWorkers(1000)
	if got < 1 {
		t.Errorf("NIX_BUILD_CORES=-1: got %d, want >= 1", got)
	}
}
