package gofiles

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ListFiles(dir, "")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files.GoFiles) != 1 || files.GoFiles[0] != "main.go" {
		t.Errorf("GoFiles = %v, want [main.go]", files.GoFiles)
	}
	if !files.IsCommand {
		t.Error("IsCommand = false, want true")
	}
}

func TestBuildContextTags(t *testing.T) {
	ctx := BuildContext("integration,debug")
	if len(ctx.BuildTags) != 2 {
		t.Errorf("BuildTags = %v, want [integration debug]", ctx.BuildTags)
	}
}

func TestNonNilSlice(t *testing.T) {
	got := nonNil(nil)
	if got == nil {
		t.Error("nonNil(nil) returned nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("nonNil(nil) = %v, want []", got)
	}
}

// writeFile is a test helper that creates a file with the given content,
// creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveEmbedCfg_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.txt"), "b")
	writeFile(t, filepath.Join(dir, "c.go"), "package x")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["*.txt"]
	slices.Sort(matched)
	if len(matched) != 2 || matched[0] != "a.txt" || matched[1] != "b.txt" {
		t.Errorf("Patterns[*.txt] = %v, want [a.txt b.txt]", matched)
	}
	if cfg.Files["a.txt"] != filepath.Join(dir, "a.txt") {
		t.Errorf("Files[a.txt] = %q, want %q", cfg.Files["a.txt"], filepath.Join(dir, "a.txt"))
	}
	if _, ok := cfg.Files["c.go"]; ok {
		t.Error("c.go should not be in Files")
	}
}

func TestResolveEmbedCfg_Directory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "index.html"), "<html>")
	writeFile(t, filepath.Join(dir, "static", "css", "style.css"), "body{}")

	cfg, err := ResolveEmbedCfg(dir, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["static"]
	slices.Sort(matched)
	want := []string{"static/css/style.css", "static/index.html"}
	if !slices.Equal(matched, want) {
		t.Errorf("Patterns[static] = %v, want %v", matched, want)
	}
	for _, f := range want {
		if cfg.Files[f] != filepath.Join(dir, f) {
			t.Errorf("Files[%s] = %q, want %q", f, cfg.Files[f], filepath.Join(dir, f))
		}
	}
}

func TestResolveEmbedCfg_HiddenFilesExcluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "visible.txt"), "ok")
	writeFile(t, filepath.Join(dir, "static", ".hidden"), "secret")
	writeFile(t, filepath.Join(dir, "static", "_private"), "secret")

	cfg, err := ResolveEmbedCfg(dir, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["static"]
	if len(matched) != 1 || matched[0] != "static/visible.txt" {
		t.Errorf("Patterns[static] = %v, want [static/visible.txt]", matched)
	}
}

func TestResolveEmbedCfg_AllPrefixIncludesHidden(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "visible.txt"), "ok")
	writeFile(t, filepath.Join(dir, "static", ".hidden"), "included")
	writeFile(t, filepath.Join(dir, "static", "_private"), "included")

	cfg, err := ResolveEmbedCfg(dir, []string{"all:static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["all:static"]
	slices.Sort(matched)
	want := []string{"static/.hidden", "static/_private", "static/visible.txt"}
	if !slices.Equal(matched, want) {
		t.Errorf("Patterns[all:static] = %v, want %v", matched, want)
	}
}

func TestResolveEmbedCfg_HiddenDirSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "ok.txt"), "ok")
	writeFile(t, filepath.Join(dir, "static", ".git", "config"), "nope")
	writeFile(t, filepath.Join(dir, "static", "_build", "out.txt"), "nope")

	cfg, err := ResolveEmbedCfg(dir, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["static"]
	if len(matched) != 1 || matched[0] != "static/ok.txt" {
		t.Errorf("Patterns[static] = %v, want [static/ok.txt]", matched)
	}
}

func TestResolveEmbedCfg_MultiplePatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.sql"), "select 1")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt", "*.sql"})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Patterns) != 2 {
		t.Fatalf("expected 2 pattern entries, got %d", len(cfg.Patterns))
	}
	if cfg.Patterns["*.txt"][0] != "a.txt" {
		t.Errorf("Patterns[*.txt] = %v, want [a.txt]", cfg.Patterns["*.txt"])
	}
	if cfg.Patterns["*.sql"][0] != "b.sql" {
		t.Errorf("Patterns[*.sql] = %v, want [b.sql]", cfg.Patterns["*.sql"])
	}
	if len(cfg.Files) != 2 {
		t.Errorf("expected 2 Files entries, got %d", len(cfg.Files))
	}
}

func TestResolveEmbedCfg_NoMatches(t *testing.T) {
	dir := t.TempDir()

	_, err := ResolveEmbedCfg(dir, []string{"*.txt"})
	if err == nil {
		t.Fatal("expected error for pattern with no matches, got nil")
	}
	if !strings.Contains(err.Error(), "no matching files found") {
		t.Errorf("expected 'no matching files found' in error, got: %v", err)
	}
}

func TestListFiles_LibraryPackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package lib\n\nvar X = 1\n")

	files, err := ListFiles(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if files.IsCommand {
		t.Error("IsCommand = true, want false for library package")
	}
	if len(files.GoFiles) != 1 || files.GoFiles[0] != "lib.go" {
		t.Errorf("GoFiles = %v, want [lib.go]", files.GoFiles)
	}
}

func TestListFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := ListFiles(dir, "")
	if err == nil {
		t.Fatal("expected error for directory with no Go files, got nil")
	}
}

func TestResolveEmbedCfg_InvalidPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")

	// "." is explicitly invalid for //go:embed
	_, err := ResolveEmbedCfg(dir, []string{"."})
	if err == nil {
		t.Fatal("expected error for invalid pattern '.', got nil")
	}
	if !strings.Contains(err.Error(), "invalid pattern syntax") {
		t.Errorf("expected 'invalid pattern syntax' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_IrregularFileRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.txt"), "real")
	// Create a symlink — should be rejected as irregular.
	os.Symlink("real.txt", filepath.Join(dir, "link.txt"))

	_, err := ResolveEmbedCfg(dir, []string{"link.txt"})
	if err == nil {
		t.Fatal("expected error for symlink, got nil")
	}
	if !strings.Contains(err.Error(), "irregular file") {
		t.Errorf("expected 'irregular file' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_ModuleBoundary(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory that contains its own go.mod — a module boundary.
	writeFile(t, filepath.Join(dir, "sub", "go.mod"), "module other\n")
	writeFile(t, filepath.Join(dir, "sub", "data.txt"), "data")

	_, err := ResolveEmbedCfg(dir, []string{"sub/data.txt"})
	if err == nil {
		t.Fatal("expected error for embedding across module boundary, got nil")
	}
	if !strings.Contains(err.Error(), "different module") {
		t.Errorf("expected 'different module' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_BadEmbedName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "config"), "bad")

	_, err := ResolveEmbedCfg(dir, []string{".git/config"})
	if err == nil {
		t.Fatal("expected error for bad embed name .git, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected 'invalid' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_DirModuleBoundary(t *testing.T) {
	dir := t.TempDir()
	// Directory embed should stop at module boundaries.
	writeFile(t, filepath.Join(dir, "data", "ok.txt"), "ok")
	writeFile(t, filepath.Join(dir, "data", "nested", "go.mod"), "module nested\n")
	writeFile(t, filepath.Join(dir, "data", "nested", "skip.txt"), "should be skipped")

	cfg, err := ResolveEmbedCfg(dir, []string{"data"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["data"]
	// Should contain ok.txt but not nested/skip.txt (behind module boundary).
	if len(matched) != 1 || matched[0] != "data/ok.txt" {
		t.Errorf("Patterns[data] = %v, want [data/ok.txt]", matched)
	}
}

func TestResolveEmbedCfg_Deduplication(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.txt"), "hello")
	writeFile(t, filepath.Join(dir, "other.txt"), "world")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt", "readme.txt"})
	if err != nil {
		t.Fatal(err)
	}

	// Both patterns should resolve, but Files should not have duplicates.
	// *.txt matches both files; readme.txt matches one.
	if len(cfg.Files) != 2 {
		t.Errorf("expected 2 unique files, got %d: %v", len(cfg.Files), cfg.Files)
	}
	// The per-pattern lists should each be correct.
	if len(cfg.Patterns["*.txt"]) != 2 {
		t.Errorf("Patterns[*.txt] = %v, want 2 entries", cfg.Patterns["*.txt"])
	}
	if len(cfg.Patterns["readme.txt"]) != 1 {
		t.Errorf("Patterns[readme.txt] = %v, want 1 entry", cfg.Patterns["readme.txt"])
	}
}
