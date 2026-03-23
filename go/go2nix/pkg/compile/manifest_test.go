package compile

import (
	"os"
	"path/filepath"
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
			json: `{"version":1,"kind":"compile","importcfgParts":["/a/importcfg","/b/importcfg"],"tags":["nethttpomithttp2"],"gcflags":["-shared"],"pgoProfile":null}`,
		},
		{
			name: "valid with pgo",
			json: `{"version":1,"kind":"compile","importcfgParts":[],"tags":[],"gcflags":[],"pgoProfile":"/nix/store/xxx-profile.pprof"}`,
		},
		{
			name:    "wrong version",
			json:    `{"version":2,"kind":"compile","importcfgParts":[],"tags":[],"gcflags":[],"pgoProfile":null}`,
			wantErr: "unsupported version 2",
		},
		{
			name:    "wrong kind",
			json:    `{"version":1,"kind":"link","importcfgParts":[],"tags":[],"gcflags":[],"pgoProfile":null}`,
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
			if m.Version != 1 {
				t.Errorf("version = %d, want 1", m.Version)
			}
			if m.Kind != "compile" {
				t.Errorf("kind = %q, want compile", m.Kind)
			}
		})
	}
}

func TestLoadCompileManifest_fields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	json := `{"version":1,"kind":"compile","importcfgParts":["/a","/b"],"tags":["foo","bar"],"gcflags":["-shared","-N"],"pgoProfile":"/pgo"}`
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
	if len(m.Tags) != 2 || m.Tags[0] != "foo" || m.Tags[1] != "bar" {
		t.Errorf("tags = %v", m.Tags)
	}
	if len(m.GCFlags) != 2 || m.GCFlags[0] != "-shared" || m.GCFlags[1] != "-N" {
		t.Errorf("gcflags = %v", m.GCFlags)
	}
	if m.PGOProfile == nil || *m.PGOProfile != "/pgo" {
		t.Errorf("pgoProfile = %v", m.PGOProfile)
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
			json: `{"version":1,"kind":"test","importcfgParts":["/a/importcfg"],"localArchives":{"example.com/foo":"/nix/store/foo.a"},"moduleRoot":"/nix/store/src","tags":[],"gcflags":[],"checkFlags":["-v"]}`,
		},
		{
			name:    "wrong kind",
			json:    `{"version":1,"kind":"compile","importcfgParts":[],"localArchives":{},"moduleRoot":"/src","tags":[],"gcflags":[],"checkFlags":[]}`,
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
			json: `{"version":1,"kind":"link","importcfgParts":["/a/importcfg"],"localArchives":{"example.com/foo":"/nix/store/foo.a"},"subPackages":["./cmd/server"],"moduleRoot":"/nix/store/src","lockfile":"/nix/store/lockfile","pname":"myapp","goos":"linux","goarch":"amd64","ldflags":["-s","-w"],"gcflags":[],"tags":[],"pgoProfile":null}`,
		},
		{
			name:    "wrong kind",
			json:    `{"version":1,"kind":"compile","importcfgParts":[],"localArchives":{},"subPackages":["."],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`,
			wantErr: `wrong kind "compile"`,
		},
		{
			name:    "wrong version",
			json:    `{"version":99,"kind":"link","importcfgParts":[],"localArchives":{},"subPackages":["."],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`,
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
			if len(m.SubPackages) != 1 || m.SubPackages[0] != "./cmd/server" {
				t.Errorf("subPackages = %v", m.SubPackages)
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
	json := `{"version":1,"kind":"link","importcfgParts":[],"localArchives":{},"subPackages":["."],"moduleRoot":"/src","lockfile":"/lock","pname":"x","goos":null,"goarch":null,"ldflags":[],"gcflags":[],"tags":[],"pgoProfile":null}`
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
