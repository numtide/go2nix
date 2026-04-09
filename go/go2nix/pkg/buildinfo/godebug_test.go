package buildinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultGODEBUG_Go121(t *testing.T) {
	dir := t.TempDir()
	gomod := "module example.com/test\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DefaultGODEBUG(dir)

	// Go 1.21 is before many Changed versions, so we expect several entries.
	if result == "" {
		t.Fatal("expected non-empty GODEBUG for go 1.21")
	}

	// Check that panicnil=1 is present (Changed: 21, so 21 < 21 is false — should NOT be present).
	// Actually 21 < 21 is false, so panicnil should NOT be in the output.
	if contains(result, "panicnil=") {
		t.Error("panicnil should not be present for go 1.21 (21 < 21 is false)")
	}

	// httplaxcontentlength=1 has Changed: 22, so 21 < 22 is true — should be present.
	if !contains(result, "httplaxcontentlength=1") {
		t.Error("expected httplaxcontentlength=1 for go 1.21")
	}

	// asynctimerchan=1 has Changed: 23, so 21 < 23 is true — should be present.
	if !contains(result, "asynctimerchan=1") {
		t.Error("expected asynctimerchan=1 for go 1.21")
	}
}

func TestDefaultGODEBUG_Go124(t *testing.T) {
	dir := t.TempDir()
	gomod := "module example.com/test\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DefaultGODEBUG(dir)

	// Go 1.24 — entries with Changed > 24 should be present.
	// asynctimerchan has Changed: 23, so 24 < 23 is false — should NOT be present.
	if contains(result, "asynctimerchan=") {
		t.Error("asynctimerchan should not be present for go 1.24")
	}

	// containermaxprocs has Changed: 25, so 24 < 25 is true — should be present.
	if !contains(result, "containermaxprocs=0") {
		t.Error("expected containermaxprocs=0 for go 1.24")
	}
}

func TestDefaultGODEBUG_LatestVersion(t *testing.T) {
	dir := t.TempDir()
	// Use a version higher than all Changed values in the table.
	gomod := "module example.com/test\n\ngo 1.99\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DefaultGODEBUG(dir)

	// No entries should have minor < Changed, so result should be empty.
	if result != "" {
		t.Errorf("expected empty GODEBUG for go 1.99, got %q", result)
	}
}

func TestDefaultGODEBUG_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	result := DefaultGODEBUG(dir)
	if result != "" {
		t.Errorf("expected empty result when no go.mod, got %q", result)
	}
}

func TestDefaultGODEBUG_GodebugDirective(t *testing.T) {
	dir := t.TempDir()
	gomod := "module example.com/test\n\ngo 1.21\n\ngodebug asynctimerchan=0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DefaultGODEBUG(dir)

	// asynctimerchan should be overridden to 0 by the explicit godebug directive.
	if !contains(result, "asynctimerchan=0") {
		t.Errorf("expected asynctimerchan=0 from godebug directive, got %q", result)
	}
}

func TestDefaultGODEBUG_DefaultDirective(t *testing.T) {
	dir := t.TempDir()
	// go 1.21 but godebug default go1.24 — should use 1.24 for defaults.
	gomod := "module example.com/test\n\ngo 1.21\n\ngodebug default=go1.24\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DefaultGODEBUG(dir)

	// With default go1.24, asynctimerchan (Changed: 23) should NOT be present
	// because 24 < 23 is false.
	if contains(result, "asynctimerchan=") {
		t.Error("asynctimerchan should not be present with godebug default=go1.24")
	}

	// But containermaxprocs (Changed: 25) should be present: 24 < 25 is true.
	if !contains(result, "containermaxprocs=0") {
		t.Error("expected containermaxprocs=0 with godebug default=go1.24")
	}
}

func TestDefaultGODEBUG_ExplicitSurvivesLaterDefault(t *testing.T) {
	// Regression for finding #9: explicit `godebug k=v` directives must
	// persist even when a `godebug default=goX.Y` directive appears later
	// in go.mod, matching cmd/go's defaultGODEBUG.
	tests := []struct {
		name  string
		gomod string
		want  map[string]string // substrings that must appear as "k=v"
		not   []string          // substrings that must NOT appear
	}{
		{
			name: "explicit before default",
			gomod: "module example.com/test\n\ngo 1.21\n\n" +
				"godebug panicnil=1\n" +
				"godebug default=go1.24\n",
			want: map[string]string{"panicnil": "1", "containermaxprocs": "0"},
			not:  []string{"asynctimerchan="},
		},
		{
			name: "explicit after default",
			gomod: "module example.com/test\n\ngo 1.21\n\n" +
				"godebug default=go1.24\n" +
				"godebug panicnil=1\n",
			want: map[string]string{"panicnil": "1", "containermaxprocs": "0"},
			not:  []string{"asynctimerchan="},
		},
		{
			name: "explicit overrides table default regardless of position",
			gomod: "module example.com/test\n\ngo 1.24\n\n" +
				"godebug asynctimerchan=0\n" +
				"godebug default=go1.21\n",
			// default=go1.21 would set asynctimerchan=1 from the table,
			// but the explicit directive must win.
			want: map[string]string{"asynctimerchan": "0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tt.gomod), 0o644); err != nil {
				t.Fatal(err)
			}
			result := DefaultGODEBUG(dir)
			for k, v := range tt.want {
				if !contains(result, k+"="+v) {
					t.Errorf("expected %s=%s in %q", k, v, result)
				}
			}
			for _, s := range tt.not {
				if contains(result, s) {
					t.Errorf("did not expect %q in %q", s, result)
				}
			}
		})
	}
}

func TestParseGoMinor(t *testing.T) {
	tests := []struct {
		v    string
		want int
	}{
		{"1.21", 21},
		{"1.21.3", 21},
		{"1.22", 22},
		{"1.0", 0},
		{"2.0", -1},
		{"invalid", -1},
		{"", -1},
	}
	for _, tt := range tests {
		got := parseGoMinor(tt.v)
		if got != tt.want {
			t.Errorf("parseGoMinor(%q) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
