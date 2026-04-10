package nixdrv

import (
	"strings"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"golang.org/x/crypto/ssh", "golang.org-x-crypto-ssh"},
		{"github.com/foo/bar@v1.2.3", "github.com-foo-bar_at_v1.2.3"},
		{"go.uber.org/zap", "go.uber.org-zap"},
		{"github.com/a+b/c", "github.com-a+b-c"},
		{"git.sr.ht/~geb/opt", "git.sr.ht-_geb-opt"},
	}
	for _, tt := range tests {
		if got := SanitizeName(tt.input); got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDrvNames(t *testing.T) {
	if got := PkgDrvName("golang.org/x/crypto/ssh", "v0.17.0"); got != "gopkg-golang.org-x-crypto-ssh-v0.17.0" {
		t.Errorf("PkgDrvName = %q", got)
	}
	if got := ModDrvName("golang.org/x/crypto@v0.17.0"); got != "gomod-golang.org-x-crypto_at_v0.17.0" {
		t.Errorf("ModDrvName = %q", got)
	}
	if got := LinkDrvName("myapp"); got != "golink-myapp" {
		t.Errorf("LinkDrvName = %q", got)
	}
	if got := CollectDrvName("myapp"); got != "gocollect-myapp" {
		t.Errorf("CollectDrvName = %q", got)
	}
}

func TestSanitizeNameLengthCap(t *testing.T) {
	// 300-char import path: "example.com/" + 288 'a's.
	long := "example.com/" + strings.Repeat("a", 288)
	if len(long) != 300 {
		t.Fatalf("test setup: len(long) = %d", len(long))
	}

	got := SanitizeName(long)
	if len(got) > maxSanitizedLen {
		t.Errorf("SanitizeName len = %d, want <= %d", len(got), maxSanitizedLen)
	}
	// Deterministic: same input → same output.
	if again := SanitizeName(long); again != got {
		t.Errorf("SanitizeName not deterministic: %q vs %q", got, again)
	}
	// Cross-implementation parity: rust/src/resolve.rs and nix/helpers.nix
	// must produce this exact string for the same input.
	want := "example.com-" + strings.Repeat("a", 139) + "-2d904ea3"
	if got != want {
		t.Errorf("SanitizeName = %q, want %q", got, want)
	}
	// Distinct long inputs must not collide.
	other := "example.com/" + strings.Repeat("b", 288)
	if SanitizeName(other) == got {
		t.Errorf("SanitizeName collision on distinct long inputs")
	}
	// Full derivation name with a worst-case pseudo-version still fits the
	// Nix store-name limit (211) with room to spare.
	drv := PkgDrvName(long, "v0.0.0-20230101120000-abcdef123456")
	if len(drv) > 211 {
		t.Errorf("PkgDrvName len = %d exceeds Nix store-name limit", len(drv))
	}
	if len(drv) > 201 {
		t.Errorf("PkgDrvName len = %d, want <= 201", len(drv))
	}
}
