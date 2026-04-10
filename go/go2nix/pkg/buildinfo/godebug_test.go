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

	result := DefaultGODEBUG(dir, nil)

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

	result := DefaultGODEBUG(dir, nil)

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

	result := DefaultGODEBUG(dir, nil)

	// No entries should have minor < Changed, so result should be empty.
	if result != "" {
		t.Errorf("expected empty GODEBUG for go 1.99, got %q", result)
	}
}

func TestDefaultGODEBUG_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	result := DefaultGODEBUG(dir, nil)
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

	result := DefaultGODEBUG(dir, nil)

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

	result := DefaultGODEBUG(dir, nil)

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
			result := DefaultGODEBUG(dir, nil)
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

func TestDefaultGODEBUG_NoGoDirective(t *testing.T) {
	dir := t.TempDir()
	gomod := "module example.com/test\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DefaultGODEBUG(dir, nil)

	// gover.FromGoMod falls back to DefaultGoModVersion = "1.16", so every
	// table entry with Changed > 16 applies. Spot-check a few across the
	// Changed range.
	for _, want := range []string{"netedns0=0", "panicnil=1", "httpmuxgo121=1", "asynctimerchan=1"} {
		if !contains(result, want) {
			t.Errorf("expected %s for go.mod with no go directive (1.16 baseline); got %q", want, result)
		}
	}
}

func TestDefaultGODEBUG_SourceDirectives(t *testing.T) {
	dir := t.TempDir()
	gomod := "module example.com/test\n\ngo 1.24\n\ngodebug panicnil=0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	src := []Godebug{
		{Key: "panicnil", Value: "1"},
		{Key: "httpmuxgo121", Value: "1"},
	}
	result := DefaultGODEBUG(dir, src)

	// Source directive overrides go.mod for the same key.
	if !contains(result, "panicnil=1") {
		t.Errorf("expected panicnil=1 (source overrides go.mod); got %q", result)
	}
	if !contains(result, "httpmuxgo121=1") {
		t.Errorf("expected httpmuxgo121=1 from source directive; got %q", result)
	}
	// Table default for go1.24 (containermaxprocs Changed=25) still applies.
	if !contains(result, "containermaxprocs=0") {
		t.Errorf("expected containermaxprocs=0 from table; got %q", result)
	}
}

func TestDefaultGODEBUG_SourceDefaultDirective(t *testing.T) {
	dir := t.TempDir()
	gomod := "module example.com/test\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	// //go:debug default=go1.24 in source overrides go.mod's go 1.21.
	result := DefaultGODEBUG(dir, []Godebug{{Key: "default", Value: "go1.24"}})

	if contains(result, "asynctimerchan=") {
		t.Errorf("asynctimerchan should not be present with source default=go1.24; got %q", result)
	}
	if !contains(result, "containermaxprocs=0") {
		t.Errorf("expected containermaxprocs=0 with source default=go1.24; got %q", result)
	}
}

func TestParseGoDebug(t *testing.T) {
	tests := []struct {
		text       string
		key, value string
		wantErr    bool
	}{
		{"//go:debug panicnil=1", "panicnil", "1", false},
		{"//go:debug\tdefault=go1.24", "default", "go1.24", false},
		{"//go:debug   httpmuxgo121=1  ", "httpmuxgo121", "1", false},
		{"//go:debug", "", "", true},
		{"//go:debug noequals", "", "", true},
		{"//go:debug k=a,b", "", "", true},
		{"//go:debugfoo=1", "", "", true},
		{"// go:debug panicnil=1", "", "", true},
		{"//go:embed all:dist", "", "", true},
	}
	for _, tt := range tests {
		k, v, err := ParseGoDebug(tt.text)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseGoDebug(%q) err=%v, wantErr=%v", tt.text, err, tt.wantErr)
			continue
		}
		if k != tt.key || v != tt.value {
			t.Errorf("ParseGoDebug(%q) = %q,%q; want %q,%q", tt.text, k, v, tt.key, tt.value)
		}
	}
}

func TestParseSourceGodebugs(t *testing.T) {
	dir := t.TempDir()
	mainGo := `// Binary x demonstrates //go:debug.
//
//go:debug panicnil=1
//go:debug httpmuxgo121=1
package main

//go:debug ignored=1
func main() {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	otherGo := `//go:debug asynctimerchan=0
package main
`
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte(otherGo), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ParseSourceGodebugs(nil, dir)

	want := map[string]string{"panicnil": "1", "httpmuxgo121": "1", "asynctimerchan": "0"}
	gotM := map[string]string{}
	for _, g := range got {
		gotM[g.Key] = g.Value
	}
	for k, v := range want {
		if gotM[k] != v {
			t.Errorf("ParseSourceGodebugs: missing or wrong %s=%s; got %v", k, v, got)
		}
	}
	if _, ok := gotM["ignored"]; ok {
		t.Errorf("ParseSourceGodebugs: directive after package clause should not be collected; got %v", got)
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
