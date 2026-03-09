package nixdrv

import "testing"

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"golang.org/x/crypto/ssh", "golang-org-x-crypto-ssh"},
		{"github.com/foo/bar@v1.2.3", "github-com-foo-bar-v1-2-3"},
		{"go.uber.org/zap", "go-uber-org-zap"},
		{"github.com/a+b/c", "github-com-a_b-c"},
	}
	for _, tt := range tests {
		if got := SanitizeName(tt.input); got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDrvNames(t *testing.T) {
	if got := PkgDrvName("golang.org/x/crypto/ssh"); got != "gopkg-golang-org-x-crypto-ssh" {
		t.Errorf("PkgDrvName = %q", got)
	}
	if got := ModDrvName("golang.org/x/crypto@v0.17.0"); got != "gomod-golang-org-x-crypto-v0-17-0" {
		t.Errorf("ModDrvName = %q", got)
	}
	if got := LinkDrvName("myapp"); got != "golink-myapp" {
		t.Errorf("LinkDrvName = %q", got)
	}
	if got := CollectDrvName("myapp"); got != "gocollect-myapp" {
		t.Errorf("CollectDrvName = %q", got)
	}
}

func TestEscapeModPath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"golang.org/x/crypto", "golang.org/x/crypto"},
		{"github.com/BurntSushi/toml", "github.com/!burnt!sushi/toml"},
		{"github.com/Azure/go-autorest", "github.com/!azure/go-autorest"},
		{"github.com/a/B/C", "github.com/a/!b/!c"},
	}
	for _, tt := range tests {
		if got := EscapeModPath(tt.input); got != tt.want {
			t.Errorf("EscapeModPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
