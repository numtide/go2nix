package resolve

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/numtide/go2nix/pkg/compile"
)

func TestFodScript(t *testing.T) {
	script := fodScript(
		"/nix/store/xxx-go/bin/go",
		"golang.org/x/crypto",
		"v0.17.0",
		"/nix/store/yyy-cacert/etc/ssl/certs/ca-bundle.crt",
		"",
	)

	if !strings.Contains(script, `GOMODCACHE="$out"`) {
		t.Error("missing GOMODCACHE=\"$out\"")
	}
	if !strings.Contains(script, `mod download 'golang.org/x/crypto@v0.17.0'`) {
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

	if !strings.Contains(script, "cp '/nix/store/zzz-netrc/netrc' \"$HOME/.netrc\"") {
		t.Error("missing netrc copy")
	}
	if !strings.Contains(script, `chmod 600 "$HOME/.netrc"`) {
		t.Error("missing netrc chmod")
	}
}

func TestFodScriptQuotesSpecialChars(t *testing.T) {
	script := fodScript(
		"/nix/store/xxx-go/bin/go",
		"example.com/foo's-bar",
		"v1.0.0",
		"",
		"",
	)

	// Single quote in fetchPath must be escaped
	if !strings.Contains(script, `'example.com/foo'\''s-bar@v1.0.0'`) {
		t.Errorf("fetchPath with single quote not properly escaped:\n%s", script)
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
	if !strings.Contains(script, "${compileManifestJSON//@@IMPORTCFG@@/$NIX_BUILD_TOP/importcfg}") {
		t.Error("missing compileManifestJSON with @@IMPORTCFG@@ expansion")
	}
	if !strings.Contains(script, "--manifest") {
		t.Error("missing --manifest flag")
	}
	if !strings.Contains(script, `"$modSrc/$relDir"`) {
		t.Error("missing modSrc/relDir source dir")
	}
	if !strings.Contains(script, "pkg.a") {
		t.Error("missing pkg.a output")
	}
	if !strings.Contains(script, `${goVersion:+--go-version "$goVersion"}`) {
		t.Error("missing goVersion conditional for -lang flag")
	}
}

func TestLinkScript(t *testing.T) {
	script := linkScript("/nix/store/xxx-go/bin/go", "myapp", "exe")

	if !strings.Contains(script, "tool link") {
		t.Error("missing go tool link")
	}
	if !strings.Contains(script, `"$out/bin/"'myapp'`) {
		t.Errorf("missing output binary path:\n%s", script)
	}
	if !strings.Contains(script, `"$mainPkg/pkg.a"`) {
		t.Error("missing main package archive reference")
	}
	if !strings.Contains(script, `export GOROOT="$goroot"`) {
		t.Error("missing GOROOT export")
	}
	if !strings.Contains(script, "-buildid=") {
		t.Error("missing -buildid flag")
	}
	if !strings.Contains(script, "-buildmode='exe'") {
		t.Error("missing -buildmode=exe")
	}
	if !strings.Contains(script, `${extld:+-extld "$extld" -linkmode external}`) {
		t.Error("missing extld conditional for cgo external linking")
	}
}

func TestLinkScriptPIE(t *testing.T) {
	script := linkScript("/nix/store/xxx-go/bin/go", "myapp", "pie")

	if !strings.Contains(script, "-buildmode='pie'") {
		t.Error("missing -buildmode=pie")
	}
}

func TestLinkScriptSanitizerFlags(t *testing.T) {
	script := linkScript("/nix/store/xxx-go/bin/go", "myapp", "exe")

	if !strings.Contains(script, "${sanitizerLinkFlags:+$sanitizerLinkFlags}") {
		t.Error("missing sanitizerLinkFlags conditional for -race/-msan/-asan propagation")
	}
}

func TestLinkScriptGodebug(t *testing.T) {
	script := linkScript("/nix/store/xxx-go/bin/go", "myapp", "exe")

	if !strings.Contains(script, "${godebugDefault:+-X=runtime.godebugDefault=$godebugDefault}") {
		t.Error("missing godebugDefault conditional for GODEBUG default linker flag")
	}
}

func TestImportcfgPlaceholderRoundTrip(t *testing.T) {
	// Build a manifest with the placeholder, same as resolve.go does.
	pgo := "/nix/store/abc-profile/default.pgo"
	m := compile.CompileManifest{
		Version:        compile.ManifestVersion,
		Kind:           compile.ManifestKindCompile,
		ImportcfgParts: []string{compile.ImportcfgPlaceholder},
		Tags:           []string{"netgo", "osusergo"},
		GCFlags:        []string{"-shared"},
		PGOProfile:     &pgo,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Run the same bash substitution that compileScript generates.
	fakeBuildTop := "/build"
	script := `printf '%s\n' "${compileManifestJSON//@@IMPORTCFG@@/$NIX_BUILD_TOP/importcfg}"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{
		"compileManifestJSON=" + string(raw),
		"NIX_BUILD_TOP=" + fakeBuildTop,
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash substitution failed: %v", err)
	}

	// Parse the result back and verify.
	var got compile.CompileManifest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\nraw output: %s", err, out)
	}
	wantPath := fakeBuildTop + "/importcfg"
	if len(got.ImportcfgParts) != 1 || got.ImportcfgParts[0] != wantPath {
		t.Errorf("importcfgParts = %v, want [%q]", got.ImportcfgParts, wantPath)
	}
	if got.Version != compile.ManifestVersion {
		t.Errorf("version = %d, want %d", got.Version, compile.ManifestVersion)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "netgo" {
		t.Errorf("tags = %v, want [netgo osusergo]", got.Tags)
	}
	if got.PGOProfile == nil || *got.PGOProfile != pgo {
		t.Errorf("pgoProfile = %v, want %q", got.PGOProfile, pgo)
	}
}

// TestBuildCompileGCFlags verifies that -shared is injected for PIE builds and
// that user-supplied flags are preserved and correctly split by whitespace.
func TestBuildCompileGCFlags(t *testing.T) {
	tests := []struct {
		name      string
		buildMode string
		gcflags   string
		want      []string
	}{
		{"pie_no_extra", "pie", "", []string{"-shared"}},
		{"pie_with_race", "pie", "-race", []string{"-shared", "-race"}},
		{"pie_with_multiple", "pie", "-race -N", []string{"-shared", "-race", "-N"}},
		{"exe_no_extra", "exe", "", nil},
		{"exe_with_race", "exe", "-race", []string{"-race"}},
		{"exe_with_multiple", "exe", "-race -N", []string{"-race", "-N"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCompileGCFlags(tt.buildMode, tt.gcflags)
			if len(got) != len(tt.want) {
				t.Fatalf("buildCompileGCFlags(%q, %q) = %v, want %v", tt.buildMode, tt.gcflags, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCollectScript(t *testing.T) {
	script := collectScript([]string{"/placeholder1", "/placeholder2"})

	if !strings.Contains(script, "'/placeholder1'/bin/*") {
		t.Errorf("missing first placeholder copy:\n%s", script)
	}
	if !strings.Contains(script, "'/placeholder2'/bin/*") {
		t.Errorf("missing second placeholder copy:\n%s", script)
	}
}
