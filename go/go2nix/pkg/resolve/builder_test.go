package resolve

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/storepath"
	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/nixdrv"
)

func TestFodScript(t *testing.T) {
	script := fodScript(
		"/nix/store/xxx-go/bin/go",
		"/nix/store/ccc-coreutils",
		"golang.org/x/crypto",
		"v0.17.0",
		"golang.org/x/crypto@v0.17.0",
		"/nix/store/yyy-cacert/etc/ssl/certs/ca-bundle.crt",
		"",
	)

	if strings.Contains(script, `GOMODCACHE="$out"`) {
		t.Error("GOMODCACHE=$out is the proxy-dependent full-tree mode; expected $TMPDIR/modcache")
	}
	if !strings.Contains(script, `GOMODCACHE="$TMPDIR/modcache"`) {
		t.Error("missing GOMODCACHE=$TMPDIR/modcache")
	}
	if !strings.Contains(script, `cp -r "$TMPDIR/modcache/"'golang.org/x/crypto@v0.17.0' "$out"`) {
		t.Errorf("missing source-tree cp to $out:\n%s", script)
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
		"/nix/store/ccc-coreutils",
		"golang.org/x/crypto",
		"v0.17.0",
		"golang.org/x/crypto@v0.17.0",
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
		"/nix/store/ccc-coreutils",
		"example.com/foo's-bar",
		"v1.0.0",
		"example.com/foo's-bar@v1.0.0",
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
	if !strings.Contains(script, `${extld:+-extld "$extld"}`) {
		t.Error("missing extld conditional")
	}
	if strings.Contains(script, "-linkmode") {
		t.Error("script forces -linkmode; cmd/link should pick it via determineLinkMode")
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

// TestExtractSanitizerFlags verifies that -race/-msan/-asan are extracted from
// gcflags while other flags (like -shared, -N) are left out.
func TestExtractSanitizerFlags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"none", "-shared -N", ""},
		{"race_only", "-race", "-race"},
		{"msan_only", "-msan", "-msan"},
		{"asan_only", "-asan", "-asan"},
		{"race_and_shared", "-shared -race", "-race"},
		{"all_three", "-race -msan -asan", "-race -msan -asan"},
		{"mixed", "-shared -race -N -asan", "-race -asan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractSanitizerFlags(tt.input); got != tt.want {
				t.Errorf("extractSanitizerFlags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestBuildImportcfg verifies that buildImportcfg produces correct packagefile
// entries: stdlib imports point to the stdlib path, non-stdlib imports use CA
// placeholders, and a missing DrvPath is detected as a bug.
func TestBuildImportcfg(t *testing.T) {
	stdlibPath := "/nix/store/stdlib123-go-stdlib"

	// Build a fake dependency drv path (must end in .drv).
	depDrvPath, err := storepath.FromAbsolutePath(
		"/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-dep-pkg.drv")
	if err != nil {
		t.Fatal(err)
	}

	cfg := Config{StdlibPath: stdlibPath}

	pkg := &ResolvedPkg{
		ImportPath: "mymod/cmd/app",
		Imports:    []string{"fmt", "mymod/internal/util"},
	}
	graph := map[string]*ResolvedPkg{
		"mymod/internal/util": {
			ImportPath: "mymod/internal/util",
			DrvPath:    depDrvPath,
		},
	}

	drv := nixdrv.NewDerivation("test-pkg", "x86_64-linux", "/bin/bash")
	if err := buildImportcfg(cfg, drv, pkg, graph); err != nil {
		t.Fatalf("buildImportcfg: %v", err)
	}

	entries := strings.Split(drv.Env()["importcfg_entries"], "\n")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}

	// fmt is not in the graph → stdlib path.
	if want := "packagefile fmt=" + stdlibPath + "/fmt.a"; entries[0] != want {
		t.Errorf("stdlib entry: got %q, want %q", entries[0], want)
	}

	// mymod/internal/util is in the graph → CA placeholder.
	ph, err := nixdrv.CAOutput(depDrvPath, "out")
	if err != nil {
		t.Fatal(err)
	}
	if want := "packagefile mymod/internal/util=" + ph.Render() + "/pkg.a"; entries[1] != want {
		t.Errorf("dep entry: got %q, want %q", entries[1], want)
	}
}

// TestBuildImportcfgMissingDrvPath verifies that buildImportcfg returns an
// error when a non-stdlib dependency has no computed .drv path yet
// (i.e., a graph construction bug).
func TestBuildImportcfgMissingDrvPath(t *testing.T) {
	cfg := Config{StdlibPath: "/nix/store/stdlib-go"}
	pkg := &ResolvedPkg{
		ImportPath: "mymod/app",
		Imports:    []string{"mymod/lib"},
	}
	graph := map[string]*ResolvedPkg{
		"mymod/lib": {ImportPath: "mymod/lib", DrvPath: nil},
	}
	drv := nixdrv.NewDerivation("test", "x86_64-linux", "/bin/bash")
	if err := buildImportcfg(cfg, drv, pkg, graph); err == nil {
		t.Fatal("expected error for nil DrvPath, got nil")
	}
}

// TestImportcfgEntriesBashWrite verifies that the multi-line importcfg_entries
// env var is correctly written to a file by the bash printf in both
// compileScript and linkScript.
func TestImportcfgEntriesBashWrite(t *testing.T) {
	entries := strings.Join([]string{
		"packagefile fmt=/nix/store/stdlib/fmt.a",
		"packagefile net/http=/nix/store/stdlib/net/http.a",
		"packagefile mymod/lib=/run/build/placeholder/pkg.a",
	}, "\n")

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "importcfg")

	// Run the same bash snippet used by both compileScript and linkScript.
	script := `printf '%s\n' "$importcfg_entries" > "$outfile"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{
		"importcfg_entries=" + entries,
		"outfile=" + outFile,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash write failed: %v\n%s", err, out)
	}

	f, err := os.Open(outFile)
	if err != nil {
		t.Fatalf("opening importcfg: %v", err)
	}
	defer f.Close() //nolint:errcheck

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning: %v", err)
	}

	want := []string{
		"packagefile fmt=/nix/store/stdlib/fmt.a",
		"packagefile net/http=/nix/store/stdlib/net/http.a",
		"packagefile mymod/lib=/run/build/placeholder/pkg.a",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), len(want), lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line[%d]: got %q, want %q", i, lines[i], w)
		}
	}
}

// TestCreateFilteredPkgDir verifies that only compilation-relevant files are
// copied and that embed files with subdirectory paths are handled correctly.
func TestCreateFilteredPkgDir(t *testing.T) {
	src := t.TempDir()

	// Create a realistic package directory with files of every type.
	allFiles := map[string]string{
		"main.go":              "package main",
		"main_cgo.go":          "package main // cgo",
		"bridge.c":             "// c file",
		"bridge.cc":            "// c++ file",
		"bridge.f":             "// fortran file",
		"asm.s":                "// asm file",
		"types.h":              "// header",
		"blob.syso":            "\x00binary",
		"templates/index.html": "<html/>",       // embed in subdir
		"templates/style.css":  "body{}",        // embed in subdir
		"unrelated.go":         "package other", // not in any file list
	}
	for rel, content := range allFiles {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pkg := &ResolvedPkg{
		ImportPath: "mymod/app",
		GoFiles:    []string{"main.go"},
		CgoFiles:   []string{"main_cgo.go"},
		CFiles:     []string{"bridge.c"},
		CXXFiles:   []string{"bridge.cc"},
		FFiles:     []string{"bridge.f"},
		SFiles:     []string{"asm.s"},
		HFiles:     []string{"types.h"},
		SysoFiles:  []string{"blob.syso"},
		EmbedFiles: []string{"templates/index.html", "templates/style.css"},
	}

	filtered, err := createFilteredPkgDir(src, pkg)
	if err != nil {
		t.Fatalf("createFilteredPkgDir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(filtered) }) //nolint:errcheck

	// All declared files must be present.
	wantPresent := []string{
		"main.go", "main_cgo.go", "bridge.c", "bridge.cc",
		"bridge.f", "asm.s", "types.h", "blob.syso",
		"templates/index.html", "templates/style.css",
	}
	for _, f := range wantPresent {
		if _, err := os.Stat(filepath.Join(filtered, f)); err != nil {
			t.Errorf("expected %s to be present: %v", f, err)
		}
	}

	// Unrelated files must NOT appear.
	if _, err := os.Stat(filepath.Join(filtered, "unrelated.go")); err == nil {
		t.Error("unrelated.go should not be in filtered dir")
	}

	// Verify content integrity for a text file and the binary blob.
	checkContent := func(rel, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(filtered, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if string(data) != want {
			t.Errorf("%s: got %q, want %q", rel, data, want)
		}
	}
	checkContent("main.go", "package main")
	checkContent("templates/index.html", "<html/>")
	checkContent("blob.syso", "\x00binary")
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
