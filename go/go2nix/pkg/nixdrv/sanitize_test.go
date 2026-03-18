package nixdrv

import "testing"

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
