package resolve

import (
	"strings"
	"testing"
)

func TestFodScript(t *testing.T) {
	script := fodScript(
		"/nix/store/xxx-go/bin/go",
		"golang.org/x/crypto",
		"v0.17.0",
		"/nix/store/yyy-cacert/etc/ssl/certs/ca-bundle.crt",
		"",
	)

	if !strings.Contains(script, "GOMODCACHE=$out") {
		t.Error("missing GOMODCACHE=$out")
	}
	if !strings.Contains(script, `mod download "golang.org/x/crypto@v0.17.0"`) {
		t.Error("missing go mod download command")
	}
	if !strings.Contains(script, "SSL_CERT_FILE") {
		t.Error("missing SSL_CERT_FILE")
	}
	if strings.Contains(script, ".netrc") {
		t.Error("should not contain .netrc when netrcFile is empty")
	}
}

func TestFodScriptWithNetrc(t *testing.T) {
	script := fodScript(
		"/nix/store/xxx-go/bin/go",
		"golang.org/x/crypto",
		"v0.17.0",
		"",
		"/nix/store/zzz-netrc/netrc",
	)

	if !strings.Contains(script, "cp /nix/store/zzz-netrc/netrc $HOME/.netrc") {
		t.Error("missing netrc copy")
	}
	if !strings.Contains(script, "chmod 600 $HOME/.netrc") {
		t.Error("missing netrc chmod")
	}
}

func TestCompileScript(t *testing.T) {
	script := compileScript("/nix/store/zzz-go2nix/bin/go2nix")

	if !strings.Contains(script, "compile-package") {
		t.Error("missing compile-package call")
	}
	if !strings.Contains(script, "$importcfg_entries") {
		t.Error("missing importcfg_entries reference")
	}
	if !strings.Contains(script, `"$modSrc/$relDir"`) {
		t.Error("missing modSrc/relDir source dir")
	}
	if !strings.Contains(script, "pkg.a") {
		t.Error("missing pkg.a output")
	}
}

func TestLinkScript(t *testing.T) {
	script := linkScript("/nix/store/xxx-go/bin/go", "myapp")

	if !strings.Contains(script, "go tool link") {
		t.Error("missing go tool link")
	}
	if !strings.Contains(script, "$out/bin/myapp") {
		t.Error("missing output binary path")
	}
	if !strings.Contains(script, "$mainPkg/pkg.a") {
		t.Error("missing main package archive reference")
	}
}

func TestCollectScript(t *testing.T) {
	script := collectScript([]string{"/placeholder1", "/placeholder2"})

	if !strings.Contains(script, "cp /placeholder1/bin/*") {
		t.Error("missing first placeholder copy")
	}
	if !strings.Contains(script, "cp /placeholder2/bin/*") {
		t.Error("missing second placeholder copy")
	}
}
