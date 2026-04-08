package compile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadCompileManifest(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "valid manifest",
			json: `{"version":2,"kind":"compile","importcfgParts":["/a/importcfg","/b/importcfg"],"gcflags":["-shared"],"pgoProfile":null}`,
		},
		{
			name: "valid with pgo",
			json: `{"version":2,"kind":"compile","importcfgParts":[],"gcflags":[],"pgoProfile":"/nix/store/xxx-profile.pprof"}`,
		},
		{
			name:    "wrong version",
			json:    `{"version":1,"kind":"compile","importcfgParts":[],"gcflags":[],"pgoProfile":null}`,
			wantErr: "unsupported version 1",
		},
		{
			name:    "wrong kind",
			json:    `{"version":2,"kind":"link","importcfgParts":[],"gcflags":[],"pgoProfile":null}`,
			wantErr: `wrong kind "link"`,
		},
		{
			name:    "invalid json",
			json:    `{not json}`,
			wantErr: "parsing compile manifest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}

			m, err := LoadCompileManifest(path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if got := err.Error(); !contains(got, tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Version != ManifestVersion {
				t.Errorf("version = %d, want %d", m.Version, ManifestVersion)
			}
			if m.Kind != "compile" {
				t.Errorf("kind = %q, want compile", m.Kind)
			}
		})
	}
}

func TestLoadCompileManifest_fields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":2,"kind":"compile","importcfgParts":["/a","/b"],"gcflags":["-shared","-N"],"pgoProfile":"/pgo"}`
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadCompileManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(m.ImportcfgParts) != 2 || m.ImportcfgParts[0] != "/a" || m.ImportcfgParts[1] != "/b" {
		t.Errorf("importcfgParts = %v", m.ImportcfgParts)
	}
	if len(m.GCFlags) != 2 || m.GCFlags[0] != "-shared" || m.GCFlags[1] != "-N" {
		t.Errorf("gcflags = %v", m.GCFlags)
	}
	if m.PGOProfile == nil || *m.PGOProfile != "/pgo" {
		t.Errorf("pgoProfile = %v", m.PGOProfile)
	}
}

func TestLoadCompileManifest_filesOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":2,"kind":"compile","importcfgParts":[],"gcflags":[],"pgoProfile":null}`
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadCompileManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files != nil {
		t.Errorf("files should be nil when absent, got %+v", m.Files)
	}
}

func TestLoadCompileManifest_files(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":2,"kind":"compile","importcfgParts":[],"gcflags":[],"pgoProfile":null,` +
		`"files":{"goFiles":["a.go","b.go"],"cgoFiles":["c.go"],"sFiles":["asm_amd64.s"],` +
		`"hFiles":["x.h"],"embedPatterns":["data/*.txt"]}}`
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadCompileManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files == nil {
		t.Fatal("files = nil, want non-nil")
	}
	if !slices.Equal(m.Files.GoFiles, []string{"a.go", "b.go"}) {
		t.Errorf("goFiles = %v", m.Files.GoFiles)
	}
	if !slices.Equal(m.Files.CgoFiles, []string{"c.go"}) {
		t.Errorf("cgoFiles = %v", m.Files.CgoFiles)
	}
	if !slices.Equal(m.Files.SFiles, []string{"asm_amd64.s"}) {
		t.Errorf("sFiles = %v", m.Files.SFiles)
	}
	if !slices.Equal(m.Files.HFiles, []string{"x.h"}) {
		t.Errorf("hFiles = %v", m.Files.HFiles)
	}
	if !slices.Equal(m.Files.EmbedPatterns, []string{"data/*.txt"}) {
		t.Errorf("embedPatterns = %v", m.Files.EmbedPatterns)
	}
}

func TestManifestFilesToPkgFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"data/a.txt", "data/b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mf := &ManifestFiles{
		GoFiles:       []string{"main.go"},
		CgoFiles:      []string{"c.go"},
		SFiles:        []string{"asm.s"},
		EmbedPatterns: []string{"data/*.txt"},
	}
	pf, err := mf.ToPkgFiles(dir)
	if err != nil {
		t.Fatalf("ToPkgFiles: %v", err)
	}

	if !slices.Equal(pf.GoFiles, []string{"main.go"}) {
		t.Errorf("GoFiles = %v", pf.GoFiles)
	}
	if !slices.Equal(pf.CgoFiles, []string{"c.go"}) {
		t.Errorf("CgoFiles = %v", pf.CgoFiles)
	}
	if !slices.Equal(pf.SFiles, []string{"asm.s"}) {
		t.Errorf("SFiles = %v", pf.SFiles)
	}
	if pf.EmbedCfg == nil {
		t.Fatal("EmbedCfg = nil, want resolved config")
	}
	if !slices.Equal(pf.EmbedFiles, []string{"data/a.txt", "data/b.txt"}) {
		t.Errorf("EmbedFiles = %v", pf.EmbedFiles)
	}
	got := pf.EmbedCfg.Patterns["data/*.txt"]
	if !slices.Equal(got, []string{"data/a.txt", "data/b.txt"}) {
		t.Errorf("EmbedCfg.Patterns[data/*.txt] = %v", got)
	}

	// No embed patterns → no resolution, EmbedCfg stays nil.
	mf2 := &ManifestFiles{GoFiles: []string{"x.go"}}
	pf2, err := mf2.ToPkgFiles(dir)
	if err != nil {
		t.Fatalf("ToPkgFiles (no embed): %v", err)
	}
	if pf2.EmbedCfg != nil || len(pf2.EmbedFiles) != 0 {
		t.Errorf("expected no embed data, got cfg=%v files=%v", pf2.EmbedCfg, pf2.EmbedFiles)
	}
}

func TestMergeImportcfg(t *testing.T) {
	dir := t.TempDir()

	// Write two importcfg part files.
	part1 := filepath.Join(dir, "part1")
	if err := os.WriteFile(part1, []byte("packagefile fmt=/nix/store/xxx/fmt.a\nimportmap old=new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	part2 := filepath.Join(dir, "part2")
	if err := os.WriteFile(part2, []byte("# comment line\n\npackagefile net/http=/nix/store/yyy/http.a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	merged, err := MergeImportcfg([]string{part1, part2}, outDir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(merged)
	if err != nil {
		t.Fatal(err)
	}

	got := string(data)
	// Should include packagefile and importmap lines, skip comments and blanks.
	want := "packagefile fmt=/nix/store/xxx/fmt.a\nimportmap old=new\npackagefile net/http=/nix/store/yyy/http.a\n"
	if got != want {
		t.Errorf("merged importcfg:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestMergeImportcfg_empty(t *testing.T) {
	outDir := t.TempDir()
	merged, err := MergeImportcfg(nil, outDir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(merged)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty file, got %q", data)
	}
}

func TestLoadTestManifest(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "valid manifest",
			json: `{"version":2,"kind":"test","importcfgParts":["/a/importcfg"],"localArchives":{"example.com/foo":"/nix/store/foo.a"},"moduleRoot":"/nix/store/src","tags":[],"gcflags":[],"checkFlags":["-v"]}`,
		},
		{
			name:    "wrong kind",
			json:    `{"version":2,"kind":"compile","importcfgParts":[],"localArchives":{},"moduleRoot":"/src","tags":[],"gcflags":[],"checkFlags":[]}`,
			wantErr: `wrong kind "compile"`,
		},
		{
			name:    "wrong version",
			json:    `{"version":99,"kind":"test","importcfgParts":[],"localArchives":{},"moduleRoot":"/src","tags":[],"gcflags":[],"checkFlags":[]}`,
			wantErr: "unsupported version 99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}

			m, err := LoadTestManifest(path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if got := err.Error(); !contains(got, tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.ModuleRoot != "/nix/store/src" {
				t.Errorf("moduleRoot = %q", m.ModuleRoot)
			}
			if len(m.LocalArchives) != 1 || m.LocalArchives["example.com/foo"] != "/nix/store/foo.a" {
				t.Errorf("localArchives = %v", m.LocalArchives)
			}
			if len(m.CheckFlags) != 1 || m.CheckFlags[0] != "-v" {
				t.Errorf("checkFlags = %v", m.CheckFlags)
			}
		})
	}
}

func TestLoadLinkManifest(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "valid manifest",
			json: `{"version":2,"kind":"link","importcfgParts":["/a/importcfg"],"localArchives":{"example.com/foo":"/nix/store/foo.a"},"subPackages":[{"path":"./cmd/server","files":{"goFiles":["main.go"]}}],"moduleRoot":"/nix/store/src","lockfile":"/nix/store/lockfile","pname":"myapp","goos":"linux","goarch":"amd64","ldflags":["-s","-w"],"gcflags":[],"tags":[],"pgoProfile":null}`,
		},
		{
			name:    "wrong kind",
			json:    `{"version":2,"kind":"compile","importcfgParts":[],"localArchives":{},"subPackages":[{"path":"."}],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`,
			wantErr: `wrong kind "compile"`,
		},
		{
			name:    "wrong version",
			json:    `{"version":99,"kind":"link","importcfgParts":[],"localArchives":{},"subPackages":[{"path":"."}],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`,
			wantErr: "unsupported version 99",
		},
		{
			name:    "invalid json",
			json:    `not json`,
			wantErr: "parsing link manifest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}

			m, err := LoadLinkManifest(path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if got := err.Error(); !contains(got, tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Pname != "myapp" {
				t.Errorf("pname = %q", m.Pname)
			}
			if m.ModuleRoot != "/nix/store/src" {
				t.Errorf("moduleRoot = %q", m.ModuleRoot)
			}
			if len(m.SubPackages) != 1 || m.SubPackages[0].Path != "./cmd/server" {
				t.Errorf("subPackages = %v", m.SubPackages)
			}
			if m.SubPackages[0].Files == nil || len(m.SubPackages[0].Files.GoFiles) != 1 || m.SubPackages[0].Files.GoFiles[0] != "main.go" {
				t.Errorf("subPackages[0].files = %+v", m.SubPackages[0].Files)
			}
			if len(m.LDFlags) != 2 || m.LDFlags[0] != "-s" || m.LDFlags[1] != "-w" {
				t.Errorf("ldflags = %v", m.LDFlags)
			}
			if m.GOOS == nil || *m.GOOS != "linux" {
				t.Errorf("goos = %v", m.GOOS)
			}
			if m.GOARCH == nil || *m.GOARCH != "amd64" {
				t.Errorf("goarch = %v", m.GOARCH)
			}
			if m.PGOProfile != nil {
				t.Errorf("pgoProfile = %v, want nil", m.PGOProfile)
			}
		})
	}
}

func TestLoadLinkManifest_nullOptionals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":2,"kind":"link","importcfgParts":[],"localArchives":{},"subPackages":[{"path":"."}],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadLinkManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.GOOS != nil {
		t.Errorf("goos should be nil, got %v", m.GOOS)
	}
	if m.GOARCH != nil {
		t.Errorf("goarch should be nil, got %v", m.GOARCH)
	}
	if m.PGOProfile != nil {
		t.Errorf("pgoProfile should be nil, got %v", m.PGOProfile)
	}
}

func TestLoadLinkManifest_iface(t *testing.T) {
	// splitInterface mode: compileImportcfgParts and localIfaces are
	// optional fields that route the main-package compile through .x
	// (export data) files instead of the .a (link object) files used
	// by the link step.
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":2,"kind":"link",` +
		`"importcfgParts":["/a/importcfg"],` +
		`"localArchives":{"example.com/foo":"/nix/store/foo.a"},` +
		`"compileImportcfgParts":["/i/importcfg"],` +
		`"localIfaces":{"example.com/foo":"/nix/store/foo.x"},` +
		`"subPackages":[{"path":"."}],"moduleRoot":"/src","lockfile":"/lock","pname":"x",` +
		`"goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadLinkManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.CompileImportcfgParts) != 1 || m.CompileImportcfgParts[0] != "/i/importcfg" {
		t.Errorf("compileImportcfgParts: got %v, want [/i/importcfg]", m.CompileImportcfgParts)
	}
	if got, want := m.LocalIfaces["example.com/foo"], "/nix/store/foo.x"; got != want {
		t.Errorf("localIfaces[example.com/foo]: got %q, want %q", got, want)
	}
	// Backwards compatibility: with the iface fields present, the
	// link-side fields must still be readable.
	if len(m.ImportcfgParts) != 1 || m.LocalArchives["example.com/foo"] != "/nix/store/foo.a" {
		t.Errorf("link-side fields lost: importcfgParts=%v localArchives=%v",
			m.ImportcfgParts, m.LocalArchives)
	}
}

func TestLoadLinkManifest_omitIface(t *testing.T) {
	// When iface fields are omitted (default mode), they parse to
	// zero values — link-binary will fall through to compileCfg = mergedCfg.
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":2,"kind":"link","importcfgParts":[],"localArchives":{},"subPackages":[{"path":"."}],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadLinkManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.CompileImportcfgParts != nil {
		t.Errorf("compileImportcfgParts should be nil, got %v", m.CompileImportcfgParts)
	}
	if m.LocalIfaces != nil {
		t.Errorf("localIfaces should be nil, got %v", m.LocalIfaces)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
